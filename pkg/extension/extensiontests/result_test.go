package extensiontests

import (
	"strings"
	"testing"
)

func TestExtensionTestResult_ToJUnit_UnknownResult(t *testing.T) {
	tests := []struct {
		name           string
		result         Result
		wantMsgContain string
	}{
		{
			name:           "empty result type produces failure output",
			result:         Result(""),
			wantMsgContain: "unknown result type",
		},
		{
			name:           "arbitrary unknown result type produces failure output",
			result:         Result("bogus"),
			wantMsgContain: "unknown result type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ExtensionTestResult{
				Name:   "some-test",
				Result: tt.result,
				Output: "some stdout",
			}
			tc := r.ToJUnit()
			if tc.FailureOutput == nil {
				t.Fatalf("ToJUnit() FailureOutput = nil, want non-nil for result %q", tt.result)
			}
			if !strings.Contains(tc.FailureOutput.Message, tt.wantMsgContain) {
				t.Errorf("FailureOutput.Message = %q, want it to contain %q", tc.FailureOutput.Message, tt.wantMsgContain)
			}
		})
	}
}

// TestExtensionTestResults_ToJUnit_UnknownResult_CountConsistency verifies that a
// result with an unknown type increments NumFailed AND produces a TestCase with
// FailureOutput set, keeping the suite count consistent with the JUnit XML content.
func TestExtensionTestResults_ToJUnit_UnknownResult_CountConsistency(t *testing.T) {
	results := ExtensionTestResults{
		{Name: "passing-test", Result: ResultPassed},
		{Name: "unknown-result-test", Result: Result(""), Output: "raw stdout"},
	}
	suite := results.ToJUnit("my-suite")

	if suite.NumTests != 2 {
		t.Errorf("NumTests = %d, want 2", suite.NumTests)
	}
	if suite.NumFailed != 1 {
		t.Errorf("NumFailed = %d, want 1", suite.NumFailed)
	}
	if len(suite.TestCases) != 2 {
		t.Fatalf("len(TestCases) = %d, want 2", len(suite.TestCases))
	}
	unknownTC := suite.TestCases[1]
	if unknownTC.FailureOutput == nil {
		t.Error("TestCase with unknown result has nil FailureOutput; NumFailed count is inconsistent with JUnit XML")
	}
}
