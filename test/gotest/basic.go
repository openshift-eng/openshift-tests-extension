package gotest

import (
	"time"

	gt "github.com/openshift-eng/openshift-tests-extension/pkg/gotest"
)

// TestSimplePass is a simple passing test
func TestSimplePass(t gt.TestContext) {
	if 1+1 != 2 {
		t.Error("basic math failed")
	}
}

// TestSimpleFail is a simple failing test
func TestSimpleFail(t gt.TestContext) {
	t.Error("this test intentionally fails")
}

// TestWithSkip demonstrates test skipping
func TestWithSkip(t gt.TestContext) {
	t.Skip("skipping this test for demonstration")
}

// TestSlowOperation simulates a slow test
func TestSlowOperation(t gt.TestContext) {
	time.Sleep(2 * time.Second)
	t.Log("completed slow operation")
}

// TestWithSubtests demonstrates subtests  
func TestWithSubtests(t gt.TestContext) {
	t.Log("Running subtests")
	// Note: Subtests work differently in the registered test approach
	if 2+2 != 4 {
		t.Error("subtest1 failed")
	}
	if 3+3 != 6 {
		t.Error("subtest2 failed")
	}
}

// TestTableDriven demonstrates table-driven tests
func TestTableDriven(t gt.TestContext) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{"double zero", 0, 0},
		{"double one", 1, 2},
		{"double five", 5, 10},
	}

	for _, tt := range tests {
		t.Log("Running test:", tt.name)
		result := tt.input * 2
		if result != tt.expected {
			t.Errorf("%s: got %d, want %d", tt.name, result, tt.expected)
		}
	}
}

// TestSigTesting demonstrates a test with sig-testing label
func TestSigTesting(t gt.TestContext) {
	t.Log("This test has the sig-testing label")
}

// TestInformingExample demonstrates an informing test
func TestInformingExample(t gt.TestContext) {
	t.Log("This is an informing test - failures won't cause suite to fail")
}

