package extensiontests

import (
	"context"
	"sync"
	"testing"
	"time"
)

const (
	schedulerTestDelay          = 10 * time.Millisecond
	defaultSchedulerTestWorkers = 2
)

const (
	conflictDatabase = "database"
	conflictNetwork  = "network"
	conflictBlocker = "blocker"
	taintGPU        = "gpu"
)

func newTestSpec(name string, isolation Isolation) *ExtensionTestSpec {
	return &ExtensionTestSpec{
		Name: name,
		Resources: Resources{
			Isolation: isolation,
		},
	}
}

// specWithRunTracking returns a spec whose Run function records execution timing via runner.
func specWithRunTracking(runner *trackingRunner, name string, isolation Isolation) *ExtensionTestSpec {
	spec := newTestSpec(name, isolation)
	spec.Run = func(ctx context.Context) *ExtensionTestResult {
		runner.runOneTest(ctx, spec)
		return &ExtensionTestResult{Result: ResultPassed, Name: name}
	}
	return spec
}

// trackingRunner tracks test execution order and timing.
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
		testDelay:  schedulerTestDelay,
	}
}

func (r *trackingRunner) runOneTest(_ context.Context, spec *ExtensionTestSpec) {
	r.mu.Lock()
	r.startTimes[spec.Name] = time.Now()
	r.testsRun = append(r.testsRun, spec.Name)
	r.mu.Unlock()

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

	return start1.Before(end2) && start2.Before(end1)
}

func assertAllTestsCompleted(t *testing.T, runner *trackingRunner, want int) {
	t.Helper()
	if got := len(runner.getTestsRun()); got != want {
		t.Errorf("expected %d tests to complete, got %d: %v", want, got, runner.getTestsRun())
	}
}

func assertOverlap(t *testing.T, runner *trackingRunner, a, b string, wantOverlap bool, msg string) {
	t.Helper()
	got := runner.wereTestsRunningSimultaneously(a, b)
	if got != wantOverlap {
		t.Errorf("%s: overlap(%q, %q) = %v, want %v", msg, a, b, got, wantOverlap)
	}
}

// runTestsWithWorkers runs tests using multiple worker goroutines.
// The loop mirrors ExtensionTestSpecs.Run worker scheduling.
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
	t.Parallel()

	test1 := newTestSpec("test1", Isolation{})
	test2 := newTestSpec("test2", Isolation{})
	test3 := newTestSpec("test3", Isolation{})

	scheduler := NewScheduler([]*ExtensionTestSpec{test1, test2, test3})
	runner := newTrackingRunner()

	runTestsWithWorkers(context.Background(), scheduler, runner, defaultSchedulerTestWorkers)

	assertAllTestsCompleted(t, runner, 3)
}

func TestScheduler_ConflictPrevention(t *testing.T) {
	runner := newTrackingRunner()

	test1 := newTestSpec("test1", Isolation{Conflict: []string{conflictDatabase}})
	test2 := newTestSpec("test2", Isolation{Conflict: []string{conflictDatabase}})
	test3 := newTestSpec("test3", Isolation{Conflict: []string{conflictNetwork}})

	scheduler := NewScheduler([]*ExtensionTestSpec{test1, test2, test3})

	runTestsWithWorkers(context.Background(), scheduler, runner, defaultSchedulerTestWorkers)

	assertAllTestsCompleted(t, runner, 3)
	assertOverlap(t, runner, "test1", "test2", false, "same conflict")
	assertOverlap(t, runner, "test1", "test3", true, "different conflicts")
}

func TestScheduler_TaintTolerationBasic(t *testing.T) {
	runner := newTrackingRunner()

	testWithTaint := newTestSpec("test-with-taint", Isolation{Taint: []string{taintGPU}})
	testWithoutToleration := newTestSpec("test-without-toleration", Isolation{})
	testWithToleration := newTestSpec("test-with-toleration", Isolation{Toleration: []string{taintGPU}})

	scheduler := NewScheduler([]*ExtensionTestSpec{testWithTaint, testWithoutToleration, testWithToleration})

	runTestsWithWorkers(context.Background(), scheduler, runner, defaultSchedulerTestWorkers)

	assertAllTestsCompleted(t, runner, 3)
	assertOverlap(t, runner, "test-with-taint", "test-with-toleration", true, "toleration permits")
	assertOverlap(t, runner, "test-with-taint", "test-without-toleration", false, "missing toleration")
}

func TestScheduler_MultipleTaintsTolerations(t *testing.T) {
	runner := newTrackingRunner()

	testMultipleTaints := newTestSpec("test-multiple-taints", Isolation{
		Taint: []string{taintGPU, conflictNetwork},
	})
	testPartialToleration := newTestSpec("test-partial-toleration", Isolation{
		Toleration: []string{taintGPU},
	})
	testFullToleration := newTestSpec("test-full-toleration", Isolation{
		Toleration: []string{taintGPU, conflictNetwork},
	})

	scheduler := NewScheduler([]*ExtensionTestSpec{testMultipleTaints, testPartialToleration, testFullToleration})

	runTestsWithWorkers(context.Background(), scheduler, runner, defaultSchedulerTestWorkers)

	assertAllTestsCompleted(t, runner, 3)
	assertOverlap(t, runner, "test-multiple-taints", "test-full-toleration", true, "full toleration")
	assertOverlap(t, runner, "test-multiple-taints", "test-partial-toleration", false, "partial toleration")
}

func TestScheduler_ConflictsAndTaints(t *testing.T) {
	runner := newTrackingRunner()

	testWithBoth := newTestSpec("test-with-both", Isolation{
		Conflict: []string{conflictDatabase},
		Taint:    []string{taintGPU},
	})
	testConflictingTolerated := newTestSpec("test-conflicting-tolerated", Isolation{
		Conflict:   []string{conflictDatabase},
		Toleration: []string{taintGPU},
	})
	testNonConflictingIntolerated := newTestSpec("test-non-conflicting-intolerated", Isolation{
		Conflict: []string{conflictNetwork},
	})

	scheduler := NewScheduler([]*ExtensionTestSpec{testWithBoth, testConflictingTolerated, testNonConflictingIntolerated})

	runTestsWithWorkers(context.Background(), scheduler, runner, defaultSchedulerTestWorkers)

	assertAllTestsCompleted(t, runner, 3)
	assertOverlap(t, runner, "test-with-both", "test-conflicting-tolerated", false, "conflict prevents overlap")
	assertOverlap(t, runner, "test-with-both", "test-non-conflicting-intolerated", false, "taint prevents overlap")
}

func TestScheduler_NoTaints(t *testing.T) {
	t.Parallel()

	test1 := newTestSpec("test1", Isolation{})
	test2 := newTestSpec("test2", Isolation{})
	test3 := newTestSpec("test3", Isolation{})

	scheduler := NewScheduler([]*ExtensionTestSpec{test1, test2, test3})
	ctx := context.Background()

	ranTest1 := scheduler.GetNextTestToRun(ctx)
	ranTest2 := scheduler.GetNextTestToRun(ctx)
	ranTest3 := scheduler.GetNextTestToRun(ctx)

	if ranTest1 == nil || ranTest2 == nil || ranTest3 == nil {
		t.Fatal("all tests without taints should be able to run")
	}

	if extra := scheduler.GetNextTestToRun(ctx); extra != nil {
		t.Errorf("expected nil after queue exhausted, got %q", extra.Name)
	}
}

func TestScheduler_TaintReferenceCounting(t *testing.T) {
	runner := newTrackingRunner()

	taintTest1 := newTestSpec("taint-test-1", Isolation{Taint: []string{taintGPU}})
	taintTest2 := newTestSpec("taint-test-2", Isolation{Taint: []string{taintGPU}})
	toleratingTest := newTestSpec("tolerating-test", Isolation{Toleration: []string{taintGPU}})
	noTolerationTest := newTestSpec("no-toleration-test", Isolation{})

	scheduler := NewScheduler([]*ExtensionTestSpec{taintTest1, taintTest2, toleratingTest, noTolerationTest})

	runTestsWithWorkers(context.Background(), scheduler, runner, 3)

	assertAllTestsCompleted(t, runner, 4)
	assertOverlap(t, runner, "taint-test-1", "taint-test-2", false, "tainted tests without toleration block each other")
	assertOverlap(t, runner, "taint-test-1", "tolerating-test", true, "tolerating test runs alongside taint")
	assertOverlap(t, runner, "taint-test-1", "no-toleration-test", false, "intolerant test blocked by active taint")
}

func TestScheduler_ContextCancellation(t *testing.T) {
	test1 := newTestSpec("test1", Isolation{Conflict: []string{conflictBlocker}})
	test2 := newTestSpec("test2", Isolation{Conflict: []string{conflictBlocker}})

	scheduler := NewScheduler([]*ExtensionTestSpec{test1, test2})
	ctx, cancel := context.WithCancel(context.Background())

	first := scheduler.GetNextTestToRun(ctx)
	if first == nil {
		t.Fatal("expected to get first test")
	}

	go func() {
		time.Sleep(schedulerTestDelay)
		cancel()
	}()

	done := make(chan *ExtensionTestSpec)
	go func() {
		done <- scheduler.GetNextTestToRun(ctx)
	}()

	select {
	case result := <-done:
		if result != nil {
			t.Error("expected nil result after context cancellation")
		}
	case <-time.After(1 * time.Second):
		t.Error("timed out waiting for context cancellation to take effect")
	}
}

func TestScheduler_ContextCancelledBeforeStart(t *testing.T) {
	t.Parallel()

	test1 := newTestSpec("test1", Isolation{})

	scheduler := NewScheduler([]*ExtensionTestSpec{test1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if result := scheduler.GetNextTestToRun(ctx); result != nil {
		t.Error("expected nil result when context is already cancelled")
	}
}

func TestScheduler_EmptyQueue(t *testing.T) {
	t.Parallel()

	scheduler := NewScheduler([]*ExtensionTestSpec{})

	if result := scheduler.GetNextTestToRun(context.Background()); result != nil {
		t.Error("expected nil for empty queue")
	}
}

func TestScheduler_MaintainsOrderWithConflicts(t *testing.T) {
	test1 := newTestSpec("test1-conflict-db", Isolation{Conflict: []string{conflictDatabase}})
	test2 := newTestSpec("test2-conflict-db", Isolation{Conflict: []string{conflictDatabase}})
	test3 := newTestSpec("test3-no-conflict", Isolation{})

	scheduler := NewScheduler([]*ExtensionTestSpec{test1, test2, test3})
	ctx := context.Background()

	firstTest := scheduler.GetNextTestToRun(ctx)
	if firstTest == nil || firstTest.Name != "test1-conflict-db" {
		t.Fatalf("expected first call to return test1-conflict-db, got %v", firstTest)
	}

	secondTest := scheduler.GetNextTestToRun(ctx)
	if secondTest == nil || secondTest.Name != "test3-no-conflict" {
		t.Fatalf("expected second call to return test3-no-conflict, got %v", secondTest)
	}

	scheduler.MarkTestComplete(test1)

	thirdTest := scheduler.GetNextTestToRun(ctx)
	if thirdTest == nil || thirdTest.Name != "test2-conflict-db" {
		t.Fatalf("expected third call to return test2-conflict-db, got %v", thirdTest)
	}
}

func TestScheduler_DoesNotMutateCallerSlice(t *testing.T) {
	t.Parallel()

	specs := []*ExtensionTestSpec{
		newTestSpec("A", Isolation{}),
		newTestSpec("B", Isolation{}),
		newTestSpec("C", Isolation{}),
	}
	s := NewScheduler(specs)
	for s.GetNextTestToRun(context.Background()) != nil {
	}
	seen := map[string]bool{}
	for _, sp := range specs {
		seen[sp.Name] = true
	}
	for _, want := range []string{"A", "B", "C"} {
		if !seen[want] {
			t.Errorf("spec %s lost from caller's slice", want)
		}
	}
}

func TestScheduler_NilSpec(t *testing.T) {
	t.Parallel()

	scheduler := NewScheduler([]*ExtensionTestSpec{})
	scheduler.MarkTestComplete(nil)
}
