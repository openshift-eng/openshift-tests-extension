package extensiontests

import (
	"fmt"
	"strings"

	"github.com/openshift-eng/openshift-tests-extension/pkg/junit"
)

func (results ExtensionTestResults) Walk(walkFn func(*ExtensionTestResult)) {
	for i := range results {
		walkFn(results[i])
	}
}

func (result ExtensionTestResult) ToJUnit() *junit.TestCase {
	tc := &junit.TestCase{
		Name:     result.Name,
		Duration: float64(result.Duration) / 1000.0,
	}
	switch result.Result {
	case ResultFailed:
		tc.FailureOutput = &junit.FailureOutput{
			Message: result.Error,
			Output:  result.Error,
		}
	case ResultSkipped:
		tc.SkipMessage = &junit.SkipMessage{
			Message: strings.Join(result.Details, "\n"),
		}
	case ResultPassed:
		tc.SystemOut = result.Output
	}

	return tc
}

func (results ExtensionTestResults) ToJUnit(suiteName string) junit.TestSuite {
	suite := junit.TestSuite{
		Name: suiteName,
	}

	results.Walk(func(result *ExtensionTestResult) {
		suite.NumTests++
		switch result.Result {
		case ResultFailed:
			suite.NumFailed++
		case ResultSkipped:
			suite.NumSkipped++
		case ResultPassed:
			// do nothing
		default:
			panic(fmt.Sprintf("unknown result type: %s", result.Result))
		}

		suite.TestCases = append(suite.TestCases, result.ToJUnit())
	})

	return suite
}
