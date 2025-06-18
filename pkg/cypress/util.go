package cypress

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	ext "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"k8s.io/apimachinery/pkg/util/sets"
)

// TestCase represents the structure of a single test case from the metadata file
type TestCase struct {
	Name     string   `json:"name"`
	Tags     []string `json:"tags,omitempty"`
	FilePath string   `json:"filePath"`
	ID       string   `json:"id"`
}

// BuildExtensionTestSpecsFromCypressMetadata loads test metadata from JSON file
// and creates ExtensionTestSpecs for each test case
func BuildExtensionTestSpecsFromCypressMetadata(filePath string) (ext.ExtensionTestSpecs, error) {
	// Read and parse the JSON file
	fileContent, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata file: %v", err)
	}

	var testCases []TestCase
	if err := json.Unmarshal(fileContent, &testCases); err != nil {
		return nil, fmt.Errorf("failed to unmarshal test metadata: %v", err)
	}

	// Convert each test case to ExtensionTestSpec
	var specs []*ext.ExtensionTestSpec
	for _, tc := range testCases {
		spec := &ext.ExtensionTestSpec{
			Name:   tc.Name,
			Labels: sets.New(tc.Tags...),
			Run: func() *ext.ExtensionTestResult {
				return runCypressTest(tc.ID, tc.FilePath)
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
func runCypressTest(testID, testFilePath string) *ext.ExtensionTestResult {
	// To run cypress tests, we need to setup the env in prow step
	// please refer to following scripts
	// xref: https://github.com/openshift/release/blob/master/ci-operator/step-registry/openshift-extended/web-tests/openshift-extended-web-tests-commands.sh
	// xref: https://github.com/rioliu-rh/openshift-tests-private/blob/95348a6f5edfc3bc081016a9131c0d2761747a3f/frontend/console-test-frontend.sh

	result := &ext.ExtensionTestResult{
		Name: testID,
	}

	rootDir, err := getTestRootDir()
	if err != nil {
		result.Result = ext.ResultFailed
		result.Error = fmt.Sprintf("failed to get test root directory: %v", err)
		return result
	}

	absPath := filepath.Join(rootDir, testFilePath)
	cmd := exec.Command("npx", "cypress", "run", "--env", fmt.Sprintf("grep=\"%s\"", testID), "--spec", absPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		result.Result = ext.ResultFailed
		result.Error = string(output)
		return result
	}

	result.Result = ext.ResultPassed
	result.Output = string(output)
	return result
}
