package extensiontests

import (
	"context"
	"sync"
	"testing"
	"time"
)

func newTestSpec(name string, isolation Isolation) *ExtensionTestSpec {
	return &ExtensionTestSpec{
		Name: name,
		Resources: Resources{
			Isolation: isolation,
		},
	}
}

// trackingRunner tracks test execution order and timing
type trackingRunner struct {
	mu         sync.Mutex
	testsRun   []string
	startTimes map[string]time.Time
	endTimes   map[string]time.Time
	testDelay  time.Duration
}

func newTrackingRunner() *trackingRunner {
	return &trackingRunner{
		startTimes: make(map[string]time.Time),
		endTimes:   make(map[string]time.Time),
		testDelay:  10 * time.Millisecond,
	}
}

func (r *trackingRunner) runOneTest(ctx context.Context, spec *ExtensionTestSpec) {
	r.mu.Lock()
	r.startTimes[spec.Name] = time.Now()
	r.testsRun = append(r.testsRun, spec.Name)
	r.mu.Unlock()

	// Simulate test execution time
	time.Sleep(r.testDelay)

	r.mu.Lock()
	r.endTimes[spec.Name] = time.Now()
	r.mu.Unlock()
}

func (r *trackingRunner) getTestsRun() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]string, len(r.testsRun))
	copy(result, r.testsRun)
	return result
}

func (r *trackingRunner) wereTestsRunningSimultaneously(test1, test2 string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	start1, ok1 := r.startTimes[test1]
	end1, okEnd1 := r.endTimes[test1]
	start2, ok2 := r.startTimes[test2]
	end2, okEnd2 := r.endTimes[test2]

	if !ok1 || !ok2 || !okEnd1 || !okEnd2 {
		return false
	}

	// Tests overlap if one started before the other ended
	return (start1.Before(end2) && start2.Before(end1))
}

// runTestsWithWorkers runs tests using multiple worker goroutines
func runTestsWithWorkers(ctx context.Context, scheduler Scheduler, runner *trackingRunner, workers int) {
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				spec := scheduler.GetNextTestToRun(ctx)
				if spec == nil {
					return
				}
				runner.runOneTest(ctx, spec)
				scheduler.MarkTestComplete(spec)
			}
		}()
	}
	wg.Wait()
}

func TestScheduler_BasicExecution(t *testing.T) {
	test1 := newTestSpec("test1", Isolation{})
	test2 := newTestSpec("test2", Isolation{})
	test3 := newTestSpec("test3", Isolation{})

	scheduler := NewScheduler([]*ExtensionTestSpec{test1, test2, test3})
	runner := newTrackingRunner()
	ctx := context.Background()

	runTestsWithWorkers(ctx, scheduler, runner, 2)

	testsRun := runner.getTestsRun()
	if len(testsRun) != 3 {
		t.Errorf("Expected 3 tests to run, got %d", len(testsRun))
	}
}

func TestScheduler_ConflictPrevention(t *testing.T) {
	runner := newTrackingRunner()

	// Create tests with same conflict
	test1 := newTestSpec("test1", Isolation{
		Conflict: []string{"database"},
	})
	test2 := newTestSpec("test2", Isolation{
		Conflict: []string{"database"},
	})
	test3 := newTestSpec("test3", Isolation{
		Conflict: []string{"network"}, // Different conflict
	})

	scheduler := NewScheduler([]*ExtensionTestSpec{test1, test2, test3})
	ctx := context.Background()

	runTestsWithWorkers(ctx, scheduler, runner, 2)

	// All tests should complete
	testsRun := runner.getTestsRun()
	if len(testsRun) != 3 {
		t.Errorf("Expected 3 tests to run, got %d: %v", len(testsRun), testsRun)
	}

	// test1 and test2 should NOT run simultaneously (same conflict)
	if runner.wereTestsRunningSimultaneously("test1", "test2") {
		t.Error("test1 and test2 should not run simultaneously (same conflict)")
	}

	// test1 and test3 CAN run simultaneously (different conflicts)
	if !runner.wereTestsRunningSimultaneously("test1", "test3") {
		t.Error("test1 and test3 should be able to run simultaneously (different conflicts)")
	}
}

func TestScheduler_TaintTolerationBasic(t *testing.T) {
	runner := newTrackingRunner()

	// Test with taint
	testWithTaint := newTestSpec("test-with-taint", Isolation{
		Taint: []string{"gpu"},
	})

	// Test without toleration (blocked until testWithTaint completes)
	testWithoutToleration := newTestSpec("test-without-toleration", Isolation{})

	// Test with toleration (can run with testWithTaint)
	testWithToleration := newTestSpec("test-with-toleration", Isolation{
		Toleration: []string{"gpu"},
	})

	scheduler := NewScheduler([]*ExtensionTestSpec{testWithTaint, testWithoutToleration, testWithToleration})
	ctx := context.Background()

	runTestsWithWorkers(ctx, scheduler, runner, 2)

	// All tests should complete
	testsRun := runner.getTestsRun()
	if len(testsRun) != 3 {
		t.Errorf("Expected all 3 tests to complete, got %d", len(testsRun))
	}

	// testWithTaint and testWithToleration can run simultaneously (toleration allows it)
	if !runner.wereTestsRunningSimultaneously("test-with-taint", "test-with-toleration") {
		t.Error("testWithTaint and testWithToleration should run simultaneously (toleration permits)")
	}

	// testWithTaint and testWithoutToleration should NOT run simultaneously (no toleration)
	if runner.wereTestsRunningSimultaneously("test-with-taint", "test-without-toleration") {
		t.Error("testWithTaint and testWithoutToleration should not run simultaneously (missing toleration)")
	}
}

func TestScheduler_MultipleTaintsTolerations(t *testing.T) {
	runner := newTrackingRunner()

	// Test with multiple taints
	testMultipleTaints := newTestSpec("test-multiple-taints", Isolation{
		Taint: []string{"gpu", "network"},
	})

	// Test that tolerates only one taint (should be blocked)
	testPartialToleration := newTestSpec("test-partial-toleration", Isolation{
		Toleration: []string{"gpu"}, // Missing "network" toleration
	})

	// Test that tolerates all taints (can run)
	testFullToleration := newTestSpec("test-full-toleration", Isolation{
		Toleration: []string{"gpu", "network"},
	})

	scheduler := NewScheduler([]*ExtensionTestSpec{testMultipleTaints, testPartialToleration, testFullToleration})
	ctx := context.Background()

	runTestsWithWorkers(ctx, scheduler, runner, 2)

	// All tests should complete
	testsRun := runner.getTestsRun()
	if len(testsRun) != 3 {
		t.Errorf("Expected all 3 tests to complete, got %d", len(testsRun))
	}

	// testMultipleTaints and testFullToleration can run simultaneously
	if !runner.wereTestsRunningSimultaneously("test-multiple-taints", "test-full-toleration") {
		t.Error("testMultipleTaints and testFullToleration should run simultaneously")
	}

	// testMultipleTaints and testPartialToleration should NOT run simultaneously
	if runner.wereTestsRunningSimultaneously("test-multiple-taints", "test-partial-toleration") {
		t.Error("testMultipleTaints and testPartialToleration should not run simultaneously (partial toleration)")
	}
}

func TestScheduler_ConflictsAndTaints(t *testing.T) {
	runner := newTrackingRunner()

	testWithBoth := newTestSpec("test-with-both", Isolation{
		Conflict: []string{"database"},
		Taint:    []string{"gpu"},
	})

	// This test conflicts with database but has GPU toleration
	testConflictingTolerated := newTestSpec("test-conflicting-tolerated", Isolation{
		Conflict:   []string{"database"}, // Conflicts with first test
		Toleration: []string{"gpu"},      // Can tolerate first test's taint
	})

	// This test doesn't conflict but lacks toleration
	testNonConflictingIntolerated := newTestSpec("test-non-conflicting-intolerated", Isolation{
		Conflict: []string{"network"}, // Different conflict
		// Cannot tolerate first test's taint
	})

	scheduler := NewScheduler([]*ExtensionTestSpec{testWithBoth, testConflictingTolerated, testNonConflictingIntolerated})
	ctx := context.Background()

	runTestsWithWorkers(ctx, scheduler, runner, 2)

	// All tests should complete
	testsRun := runner.getTestsRun()
	if len(testsRun) != 3 {
		t.Errorf("Expected all 3 tests to complete, got %d", len(testsRun))
	}

	// testWithBoth and testConflictingTolerated should NOT run simultaneously (conflict prevents it)
	if runner.wereTestsRunningSimultaneously("test-with-both", "test-conflicting-tolerated") {
		t.Error("testWithBoth and testConflictingTolerated should not run simultaneously (conflict)")
	}

	// testWithBoth and testNonConflictingIntolerated should NOT run simultaneously (taint prevents it)
	if runner.wereTestsRunningSimultaneously("test-with-both", "test-non-conflicting-intolerated") {
		t.Error("testWithBoth and testNonConflictingIntolerated should not run simultaneously (taint)")
	}
}

func TestScheduler_NoTaints(t *testing.T) {
	// Tests with no taints or tolerations
	test1 := newTestSpec("test1", Isolation{})
	test2 := newTestSpec("test2", Isolation{})
	test3 := newTestSpec("test3", Isolation{})

	scheduler := NewScheduler([]*ExtensionTestSpec{test1, test2, test3})
	ctx := context.Background()

	// All tests should be able to run immediately (no blocking)
	ranTest1 := scheduler.GetNextTestToRun(ctx)
	ranTest2 := scheduler.GetNextTestToRun(ctx)
	ranTest3 := scheduler.GetNextTestToRun(ctx)

	if ranTest1 == nil || ranTest2 == nil || ranTest3 == nil {
		t.Error("All tests without taints should be able to run")
	}
}

func TestScheduler_TaintReferenceCounting(t *testing.T) {
	runner := newTrackingRunner()

	// Two tests with same taint
	taintTest1 := newTestSpec("taint-test-1", Isolation{
		Taint: []string{"gpu"},
	})
	taintTest2 := newTestSpec("taint-test-2", Isolation{
		Taint: []string{"gpu"},
	})

	// Test that tolerates the taint
	toleratingTest := newTestSpec("tolerating-test", Isolation{
		Toleration: []string{"gpu"},
	})

	// Test without toleration
	noTolerationTest := newTestSpec("no-toleration-test", Isolation{})

	scheduler := NewScheduler([]*ExtensionTestSpec{taintTest1, taintTest2, toleratingTest, noTolerationTest})
	ctx := context.Background()

	runTestsWithWorkers(ctx, scheduler, runner, 3)

	// All tests should complete
	testsRun := runner.getTestsRun()
	if len(testsRun) != 4 {
		t.Errorf("Expected all 4 tests to complete, got %d", len(testsRun))
	}
}

func TestScheduler_ContextCancellation(t *testing.T) {
	test1 := newTestSpec("test1", Isolation{
		Conflict: []string{"blocker"},
	})
	test2 := newTestSpec("test2", Isolation{
		Conflict: []string{"blocker"},
	})

	scheduler := NewScheduler([]*ExtensionTestSpec{test1, test2})
	ctx, cancel := context.WithCancel(context.Background())

	// Get first test
	first := scheduler.GetNextTestToRun(ctx)
	if first == nil {
		t.Fatal("Expected to get first test")
	}

	// Cancel context before second test can run
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	// Second test should return nil due to cancellation
	// (it would block waiting for conflict to clear, but context is cancelled)
	done := make(chan *ExtensionTestSpec)
	go func() {
		done <- scheduler.GetNextTestToRun(ctx)
	}()

	select {
	case result := <-done:
		if result != nil {
			t.Error("Expected nil result after context cancellation")
		}
	case <-time.After(1 * time.Second):
		t.Error("Timed out waiting for context cancellation to take effect")
	}
}

func TestScheduler_ContextCancelledBeforeStart(t *testing.T) {
	test1 := newTestSpec("test1", Isolation{})

	scheduler := NewScheduler([]*ExtensionTestSpec{test1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before starting

	result := scheduler.GetNextTestToRun(ctx)
	if result != nil {
		t.Error("Expected nil result when context is already cancelled")
	}
}

func TestScheduler_EmptyQueue(t *testing.T) {
	scheduler := NewScheduler([]*ExtensionTestSpec{})
	ctx := context.Background()

	result := scheduler.GetNextTestToRun(ctx)
	if result != nil {
		t.Error("Expected nil for empty queue")
	}
}

func TestScheduler_MaintainsOrderWithConflicts(t *testing.T) {
	// test1 and test2 conflict, test3 doesn't conflict
	// Expected: scheduler skips test2 and returns test3, maintaining test2's position
	test1 := newTestSpec("test1-conflict-db", Isolation{Conflict: []string{"database"}})
	test2 := newTestSpec("test2-conflict-db", Isolation{Conflict: []string{"database"}})
	test3 := newTestSpec("test3-no-conflict", Isolation{})

	scheduler := NewScheduler([]*ExtensionTestSpec{test1, test2, test3})
	ctx := context.Background()

	// Step 1: Get test1 (marks database conflict as running)
	firstTest := scheduler.GetNextTestToRun(ctx)
	if firstTest == nil || firstTest.Name != "test1-conflict-db" {
		t.Errorf("Expected first call to return test1-conflict-db, got %v", firstTest)
	}

	// Step 2: Get next test while test1 is "running"
	// Should skip test2 (conflicts) and return test3
	secondTest := scheduler.GetNextTestToRun(ctx)
	if secondTest == nil || secondTest.Name != "test3-no-conflict" {
		t.Errorf("Expected second call to return test3-no-conflict, got %v", secondTest)
	}

	// Step 3: Clean up test1's conflict
	scheduler.MarkTestComplete(test1)

	// Step 4: Now test2 should be runnable
	thirdTest := scheduler.GetNextTestToRun(ctx)
	if thirdTest == nil || thirdTest.Name != "test2-conflict-db" {
		t.Errorf("Expected third call to return test2-conflict-db, got %v", thirdTest)
	}
}

func TestScheduler_IsolationMode(t *testing.T) {
	// Tests in different modes should have separate conflict groups
	test1 := newTestSpec("test1-mode-a", Isolation{
		Mode:     "modeA",
		Conflict: []string{"resource"},
	})
	test2 := newTestSpec("test2-mode-b", Isolation{
		Mode:     "modeB",
		Conflict: []string{"resource"}, // Same conflict name but different mode
	})

	scheduler := NewScheduler([]*ExtensionTestSpec{test1, test2})
	ctx := context.Background()

	// Both should be able to run because they're in different conflict groups
	first := scheduler.GetNextTestToRun(ctx)
	second := scheduler.GetNextTestToRun(ctx)

	if first == nil || second == nil {
		t.Error("Both tests should be runnable (different modes = different conflict groups)")
	}
}

func TestScheduler_NilSpec(t *testing.T) {
	// MarkTestComplete should handle nil gracefully
	scheduler := NewScheduler([]*ExtensionTestSpec{})
	scheduler.MarkTestComplete(nil) // Should not panic
}

