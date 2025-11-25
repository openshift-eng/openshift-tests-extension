package gotest

import (
	"context"
	"fmt"
	"strings"

	ext "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"
)

// TestContext is the interface provided to test functions
// It provides the essential testing methods without requiring testing.TB
type TestContext interface {
	Error(args ...interface{})
	Errorf(format string, args ...interface{})
	Fail()
	FailNow()
	Failed() bool
	Fatal(args ...interface{})
	Fatalf(format string, args ...interface{})
	Helper()
	Log(args ...interface{})
	Logf(format string, args ...interface{})
	Name() string
	Skip(args ...interface{})
	SkipNow()
	Skipf(format string, args ...interface{})
	Skipped() bool
}

// TestFunc is the signature for a registered test function
type TestFunc func(t TestContext)

// Test represents a test that will be executed
type Test struct {
	Name        string
	Description string
	Labels      []string
	Lifecycle   ext.Lifecycle
	TestFunc    TestFunc
}

// BuildExtensionTestSpecsFromGoTests creates ExtensionTestSpecs from Go tests.
// Tests are provided as a slice of Test structs.
func BuildExtensionTestSpecsFromGoTests(tests []Test) (ext.ExtensionTestSpecs, error) {
	var specs []*ext.ExtensionTestSpec

	for _, test := range tests {
		// Capture test in closure
		testCopy := test
		
		// Set default lifecycle if not specified
		lifecycle := testCopy.Lifecycle
		if lifecycle == "" {
			lifecycle = ext.LifecycleBlocking
		}

		spec := &ext.ExtensionTestSpec{
			Name:      testCopy.Name,
			Labels:    sets.New(testCopy.Labels...),
			Lifecycle: lifecycle,
			Run: func(ctx context.Context) *ext.ExtensionTestResult {
				return runRegisteredTest(ctx, testCopy)
			},
			RunParallel: func(ctx context.Context) *ext.ExtensionTestResult {
				return runRegisteredTest(ctx, testCopy)
			},
		}
		specs = append(specs, spec)
	}

	return specs, nil
}

// runRegisteredTest executes a test function and captures its result
func runRegisteredTest(ctx context.Context, test Test) *ext.ExtensionTestResult {
	result := &ext.ExtensionTestResult{
		Name:      test.Name,
		Lifecycle: test.Lifecycle,
	}

	// Create a mock testing.T to capture test results
	mockT := &mockTestingT{
		name: test.Name,
	}

	// Run the test function with panic recovery
	func() {
		defer func() {
			if r := recover(); r != nil {
				mockT.failed = true
				mockT.output.WriteString(fmt.Sprintf("\npanic: %v\n", r))
			}
		}()

		test.TestFunc(mockT)
	}()

	// Determine result based on mock testing.T state
	result.Output = mockT.output.String()

	if mockT.skipped {
		result.Result = ext.ResultSkipped
		if mockT.skipMessage != "" {
			result.Output += fmt.Sprintf("\n--- SKIP: %s\n    %s\n", test.Name, mockT.skipMessage)
		}
	} else if mockT.failed {
		result.Result = ext.ResultFailed
		result.Error = mockT.output.String()
	} else {
		result.Result = ext.ResultPassed
	}

	return result
}

// mockTestingT implements a minimal testing.T interface for running tests
type mockTestingT struct {
	name        string
	failed      bool
	skipped     bool
	skipMessage string
	output      strings.Builder
}

func (m *mockTestingT) Error(args ...interface{}) {
	m.failed = true
	m.output.WriteString(fmt.Sprint(args...))
	m.output.WriteString("\n")
}

func (m *mockTestingT) Errorf(format string, args ...interface{}) {
	m.failed = true
	m.output.WriteString(fmt.Sprintf(format, args...))
	m.output.WriteString("\n")
}

func (m *mockTestingT) Fail() {
	m.failed = true
}

func (m *mockTestingT) FailNow() {
	m.failed = true
	panic("FailNow called")
}

func (m *mockTestingT) Failed() bool {
	return m.failed
}

func (m *mockTestingT) Fatal(args ...interface{}) {
	m.failed = true
	m.output.WriteString(fmt.Sprint(args...))
	m.output.WriteString("\n")
	panic("Fatal called")
}

func (m *mockTestingT) Fatalf(format string, args ...interface{}) {
	m.failed = true
	m.output.WriteString(fmt.Sprintf(format, args...))
	m.output.WriteString("\n")
	panic("Fatalf called")
}

func (m *mockTestingT) Helper() {}

func (m *mockTestingT) Log(args ...interface{}) {
	m.output.WriteString(fmt.Sprint(args...))
	m.output.WriteString("\n")
}

func (m *mockTestingT) Logf(format string, args ...interface{}) {
	m.output.WriteString(fmt.Sprintf(format, args...))
	m.output.WriteString("\n")
}

func (m *mockTestingT) Name() string {
	return m.name
}

func (m *mockTestingT) Skip(args ...interface{}) {
	m.skipped = true
	m.skipMessage = fmt.Sprint(args...)
	panic("Skip called")
}

func (m *mockTestingT) SkipNow() {
	m.skipped = true
	panic("SkipNow called")
}

func (m *mockTestingT) Skipf(format string, args ...interface{}) {
	m.skipped = true
	m.skipMessage = fmt.Sprintf(format, args...)
	panic("Skipf called")
}

func (m *mockTestingT) Skipped() bool {
	return m.skipped
}

