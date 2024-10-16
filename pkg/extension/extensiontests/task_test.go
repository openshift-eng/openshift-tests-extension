package extensiontests

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOneTimeTask_RunActuallyOnlyRunsOnce(t *testing.T) {
	actualExecutionCount := 0
	task := &OneTimeTask{
		fn: func() {
			actualExecutionCount++
		},
	}

	var wg sync.WaitGroup
	maxConcurrency := 10
	throttle := make(chan struct{}, maxConcurrency)

	for i := 0; i < 1000; i++ {
		wg.Add(1)
		throttle <- struct{}{}

		go func() {
			defer wg.Done()
			task.Run()
			<-throttle
		}()
	}
	wg.Wait()

	assert.Equal(t, task.executed, int32(1))
	assert.Equal(t, actualExecutionCount, 1)
}

func TestSpecTask_RunMustNotMutate(t *testing.T) {
	originalName := "this is a test"
	mySpec := ExtensionTestSpec{Name: originalName}
	task := &SpecTask{
		fn: func(spec ExtensionTestSpec) {
			assert.Equal(t, mySpec.Name, spec.Name)
			spec.Name = "trying to mutate the spec should fail"
		},
	}
	task.Run(mySpec)
	assert.Equal(t, mySpec.Name, originalName)
}

func TestTestResultTask_RunMayMutate(t *testing.T) {
	myRes := &ExtensionTestResult{Name: "this is a test", Result: ResultPassed}
	task := &TestResultTask{
		fn: func(result *ExtensionTestResult) {
			result.Result = ResultFailed
		},
	}
	task.Run(myRes)
	assert.Equal(t, myRes.Result, ResultFailed)
}
