package cypress

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	ext "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"
)

// TestCase represents the structure of a single test case from the metadata file
type TestCase struct {
	Name     string   `json:"name"`
	Tags     []string `json:"tags,omitempty"`
	FilePath string   `json:"filePath"`
	ID       string   `json:"id"`
}

// TestSuite represents a single test suite in JUnit XML output
type TestSuite struct {
	Name      string `xml:"name,attr"`
	Tests     int    `xml:"tests,attr"`
	TestCases []struct {
		Name      string `xml:"name,attr"`
		ClassName string `xml:"classname,attr"`
		Time      string `xml:"time,attr"`
		Failure   string `xml:"failure"`
		Skipped   string `xml:"skipped"`
	} `xml:"testcase"`
}

// TestSuites represents the root element of JUnit XML output
type TestSuites struct {
	TestSuites []TestSuite `xml:"testsuite"`
}

// BuildExtensionTestSpecsFromCypressMetadata loads test metadata from JSON bytes
// and creates ExtensionTestSpecs for each test case
func BuildExtensionTestSpecsFromCypressMetadata(metadata []byte) (ext.ExtensionTestSpecs, error) {
	var testCases []TestCase
	if err := json.Unmarshal(metadata, &testCases); err != nil {
		return nil, fmt.Errorf("failed to unmarshal test metadata: %v", err)
	}

	// Convert each test case to ExtensionTestSpec
	var specs []*ext.ExtensionTestSpec
	for _, tc := range testCases {
		spec := &ext.ExtensionTestSpec{
			Name:          tc.Name,
			Labels:        sets.New(tc.Tags...),
			CodeLocations: []string{tc.FilePath},
			Run: func(ctx context.Context) *ext.ExtensionTestResult {
				return runCypressTest(tc.ID, tc.Name, tc.FilePath)
			},
			RunParallel: func(ctx context.Context) *ext.ExtensionTestResult {
				// this is equivalent to before, but potentially could be improved.
				return runCypressTest(tc.ID, tc.Name, tc.FilePath)
			},
		}
		specs = append(specs, spec)
	}

	return specs, nil
}

// getTestRootDir returns the root directory for test files
func getTestRootDir() (string, error) {
	if root := os.Getenv("TEST_ROOT_DIR"); root != "" {
		return root, nil
	}
	// Fallback to current directory
	return os.Getwd()
}

// runCypressTest executes a single cypress test and returns the result
func runCypressTest(testID, testName, testFilePath string) *ext.ExtensionTestResult {
	result := &ext.ExtensionTestResult{
		Name: testName,
	}

	// Create temp file for XML output
	outputFile, err := os.CreateTemp("", "cypress-junit-*.xml")
	if err != nil {
		result.Result = ext.ResultFailed
		result.Error = fmt.Sprintf("failed to create temp file: %v", err)
		return result
	}
	defer os.Remove(outputFile.Name()) // Clean up

	rootDir, err := getTestRootDir()
	if err != nil {
		result.Result = ext.ResultFailed
		result.Error = fmt.Sprintf("failed to get test root directory: %v", err)
		return result
	}

	absPath := filepath.Join(rootDir, testFilePath)
	cmd := exec.Command("npx", "cypress", "run",
		"--reporter", "junit",
		"--reporter-options", fmt.Sprintf("mochaFile=%s", outputFile.Name()),
		"--env", fmt.Sprintf("grep=\"%s\"", testID),
		"--spec", absPath)

	// Capture command output
	cmdOutput, err := cmd.CombinedOutput()
	if err != nil {
		result.Result = ext.ResultFailed
		result.Error = string(cmdOutput)
		result.Output = string(cmdOutput)
		return result
	}

	// Parse XML output from Cypress JUnit reporter
	xmlData, err := os.ReadFile(outputFile.Name())
	if err != nil {
		result.Result = ext.ResultFailed
		result.Error = fmt.Sprintf("failed to read output file: %v", err)
		result.Output = string(cmdOutput)
		return result
	}

	var suites TestSuites
	if err := xml.Unmarshal(xmlData, &suites); err != nil {
		result.Result = ext.ResultFailed
		result.Error = fmt.Sprintf("failed to parse XML: %v", err)
		result.Output = string(cmdOutput)
		return result
	}

	// Process test results from all test suites looking for our specific test case
	// Cypress JUnit output may contain multiple suites and test cases
	for _, suite := range suites.TestSuites {
		for _, testCase := range suite.TestCases {
			if strings.Contains(testCase.Name, testID) || strings.Contains(testCase.ClassName, testID) {
				if testCase.Failure != "" {
					result.Result = ext.ResultFailed
					result.Error = testCase.Failure
				} else if testCase.Skipped != "" {
					result.Result = ext.ResultSkipped
					result.Error = "test was skipped"
				} else {
					result.Result = ext.ResultPassed
				}
				result.Output = string(cmdOutput)
				return result
			}
		}
	}

	// If we get here, the test case wasn't found in Cypress's JUnit output
	// This could mean the test wasn't executed or the grep filter failed
	result.Result = ext.ResultFailed
	result.Error = fmt.Sprintf("test case %s not found in results - check test name and grep filter", testID)
	result.Output = string(cmdOutput)
	return result
}
