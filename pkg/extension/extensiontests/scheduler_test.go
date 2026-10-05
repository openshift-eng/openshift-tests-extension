package extensiontests

import (
	"context"
	"fmt"
	"sort"
	"strings"
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
	conflictBlocker  = "blocker"
	taintGPU         = "gpu"
)

func mustNewScheduler(t *testing.T, tests []*ExtensionTestSpec, opts ...SchedulerOption) Scheduler {
	t.Helper()
	s, err := NewScheduler(tests, opts...)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	return s
}

func newTestSpec(name string, isolation Isolation) *ExtensionTestSpec {
	return &ExtensionTestSpec{
		Name: name,
		Resources: Resources{
			Isolation: isolation,
		},
	}
}

func newTestSpecWithResourcePools(name string, isolation Isolation, pools map[string]int) *ExtensionTestSpec {
	return &ExtensionTestSpec{
		Name: name,
		Resources: Resources{
			Isolation:     isolation,
			ResourcePools: pools,
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

func (r *trackingRunner) peakConcurrency() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	type event struct {
		t     time.Time
		delta int // +1 for start, -1 for end
	}
	var events []event
	for _, name := range r.testsRun {
		events = append(events, event{r.startTimes[name], 1})
		events = append(events, event{r.endTimes[name], -1})
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].t.Equal(events[j].t) {
			return events[i].delta < events[j].delta // ends before starts at same time
		}
		return events[i].t.Before(events[j].t)
	})
	peak, running := 0, 0
	for _, e := range events {
		running += e.delta
		if running > peak {
			peak = running
		}
	}
	return peak
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

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2, test3})
	runner := newTrackingRunner()

	runTestsWithWorkers(context.Background(), scheduler, runner, defaultSchedulerTestWorkers)

	assertAllTestsCompleted(t, runner, 3)
}

func TestScheduler_ConflictPrevention(t *testing.T) {
	runner := newTrackingRunner()

	test1 := newTestSpec("test1", Isolation{Conflict: []string{conflictDatabase}})
	test2 := newTestSpec("test2", Isolation{Conflict: []string{conflictDatabase}})
	test3 := newTestSpec("test3", Isolation{Conflict: []string{conflictNetwork}})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2, test3})

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

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{testWithTaint, testWithoutToleration, testWithToleration})

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

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{testMultipleTaints, testPartialToleration, testFullToleration})

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

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{testWithBoth, testConflictingTolerated, testNonConflictingIntolerated})

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

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2, test3})
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

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{taintTest1, taintTest2, toleratingTest, noTolerationTest})

	runTestsWithWorkers(context.Background(), scheduler, runner, 3)

	assertAllTestsCompleted(t, runner, 4)
	assertOverlap(t, runner, "taint-test-1", "taint-test-2", false, "tainted tests without toleration block each other")
	assertOverlap(t, runner, "taint-test-1", "tolerating-test", true, "tolerating test runs alongside taint")
	assertOverlap(t, runner, "taint-test-1", "no-toleration-test", false, "intolerant test blocked by active taint")
}

func TestScheduler_ContextCancellation(t *testing.T) {
	test1 := newTestSpec("test1", Isolation{Conflict: []string{conflictBlocker}})
	test2 := newTestSpec("test2", Isolation{Conflict: []string{conflictBlocker}})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2})
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

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if result := scheduler.GetNextTestToRun(ctx); result != nil {
		t.Error("expected nil result when context is already cancelled")
	}
}

func TestScheduler_EmptyQueue(t *testing.T) {
	t.Parallel()

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{})

	if result := scheduler.GetNextTestToRun(context.Background()); result != nil {
		t.Error("expected nil for empty queue")
	}
}

func TestScheduler_MaintainsOrderWithConflicts(t *testing.T) {
	test1 := newTestSpec("test1-conflict-db", Isolation{Conflict: []string{conflictDatabase}})
	test2 := newTestSpec("test2-conflict-db", Isolation{Conflict: []string{conflictDatabase}})
	test3 := newTestSpec("test3-no-conflict", Isolation{})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2, test3})
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
	s := mustNewScheduler(t, specs)
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

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{})
	scheduler.MarkTestComplete(nil)
}

// --- Pool scheduling tests ---

func TestScheduler_PoolCapacityEnforcement(t *testing.T) {
	runner := newTrackingRunner()

	// Pool has 3 units. Two tests each demand 2 — only one can run at a time.
	test1 := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"res": 2})
	test2 := newTestSpecWithResourcePools("test2", Isolation{}, map[string]int{"res": 2})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2},
		WithResourcePoolCapacity(map[string]int{"res": 3}))

	runTestsWithWorkers(context.Background(), scheduler, runner, 2)

	assertAllTestsCompleted(t, runner, 2)
	assertOverlap(t, runner, "test1", "test2", false, "pool capacity prevents overlap")
}

func TestScheduler_PoolAllowsParallelWhenCapacitySufficient(t *testing.T) {
	runner := newTrackingRunner()

	// Pool has 4 units. Two tests each demand 2 — both can run simultaneously.
	test1 := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"res": 2})
	test2 := newTestSpecWithResourcePools("test2", Isolation{}, map[string]int{"res": 2})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2},
		WithResourcePoolCapacity(map[string]int{"res": 4}))

	runTestsWithWorkers(context.Background(), scheduler, runner, 2)

	assertAllTestsCompleted(t, runner, 2)
	assertOverlap(t, runner, "test1", "test2", true, "sufficient capacity allows overlap")
}

func TestScheduler_PoolZeroDemandBypass(t *testing.T) {
	runner := newTrackingRunner()

	// test1 consumes pool, test2 has no pool demand — runs freely alongside.
	test1 := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"res": 2})
	test2 := newTestSpec("test2", Isolation{})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2},
		WithResourcePoolCapacity(map[string]int{"res": 2}))

	runTestsWithWorkers(context.Background(), scheduler, runner, 2)

	assertAllTestsCompleted(t, runner, 2)
	assertOverlap(t, runner, "test1", "test2", true, "zero-demand test bypasses pool check")
}

func TestScheduler_PoolFIFOUnderContention(t *testing.T) {
	// Pool has 3 units. test1 (demand=3) runs first, consuming all capacity.
	// test2 (demand=1) and test3 (demand=1) are queued. When test1 completes,
	// test2 should run before test3 (FIFO order preserved).
	test1 := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"res": 3})
	test2 := newTestSpecWithResourcePools("test2", Isolation{}, map[string]int{"res": 1})
	test3 := newTestSpecWithResourcePools("test3", Isolation{}, map[string]int{"res": 1})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2, test3},
		WithResourcePoolCapacity(map[string]int{"res": 3}))

	ctx := context.Background()

	// test1 takes all capacity
	first := scheduler.GetNextTestToRun(ctx)
	if first == nil || first.Name != "test1" {
		t.Fatalf("expected test1, got %v", first)
	}

	// test2 and test3 are blocked — but test2 is first in queue, so when capacity
	// returns it should be dispatched first. Use a single worker to verify ordering.
	scheduler.MarkTestComplete(first)

	second := scheduler.GetNextTestToRun(ctx)
	if second == nil || second.Name != "test2" {
		t.Fatalf("expected test2 (FIFO), got %v", second)
	}

	third := scheduler.GetNextTestToRun(ctx)
	if third == nil || third.Name != "test3" {
		t.Fatalf("expected test3, got %v", third)
	}
}

func TestScheduler_PoolWithConflictsAndTaints(t *testing.T) {
	runner := newTrackingRunner()

	// test1: uses pool + has taint. test2: uses pool + tolerates taint.
	// Pool has 4 units, so both could run from a capacity standpoint,
	// and test2 tolerates the taint, so they should overlap.
	test1 := newTestSpecWithResourcePools("test1", Isolation{Taint: []string{taintGPU}}, map[string]int{"res": 2})
	test2 := newTestSpecWithResourcePools("test2", Isolation{Toleration: []string{taintGPU}}, map[string]int{"res": 2})
	// test3: uses pool but does NOT tolerate taint — blocked by taint, not by pool.
	test3 := newTestSpecWithResourcePools("test3", Isolation{}, map[string]int{"res": 1})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2, test3},
		WithResourcePoolCapacity(map[string]int{"res": 5}))

	runTestsWithWorkers(context.Background(), scheduler, runner, 3)

	assertAllTestsCompleted(t, runner, 3)
	assertOverlap(t, runner, "test1", "test2", true, "pool + taint toleration allows overlap")
	assertOverlap(t, runner, "test1", "test3", false, "taint blocks test3 despite pool capacity")
}

func TestScheduler_PoolMultiplePools(t *testing.T) {
	runner := newTrackingRunner()

	// Two pools: "a" (cap=2) and "b" (cap=2).
	// test1 needs 2 of "a", test2 needs 2 of "b" — different pools, should overlap.
	test1 := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"a": 2})
	test2 := newTestSpecWithResourcePools("test2", Isolation{}, map[string]int{"b": 2})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2},
		WithResourcePoolCapacity(map[string]int{"a": 2, "b": 2}))

	runTestsWithWorkers(context.Background(), scheduler, runner, 2)

	assertAllTestsCompleted(t, runner, 2)
	assertOverlap(t, runner, "test1", "test2", true, "different pools don't block each other")
}

func TestScheduler_PoolMultiplePoolsSameTest(t *testing.T) {
	runner := newTrackingRunner()

	// test1 needs 1 of "a" and 1 of "b". test2 also needs 1 of each.
	// Both pools have capacity 1, so tests must serialize.
	test1 := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"a": 1, "b": 1})
	test2 := newTestSpecWithResourcePools("test2", Isolation{}, map[string]int{"a": 1, "b": 1})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2},
		WithResourcePoolCapacity(map[string]int{"a": 1, "b": 1}))

	runTestsWithWorkers(context.Background(), scheduler, runner, 2)

	assertAllTestsCompleted(t, runner, 2)
	assertOverlap(t, runner, "test1", "test2", false, "both pools at capacity prevents overlap")
}

func TestScheduler_PoolOverDemandRejected(t *testing.T) {
	t.Parallel()

	test1 := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"res": 5})

	_, err := NewScheduler([]*ExtensionTestSpec{test1},
		WithResourcePoolCapacity(map[string]int{"res": 3}))
	if err == nil {
		t.Fatal("expected error for over-demand")
	}
	if !strings.Contains(err.Error(), "demands") || !strings.Contains(err.Error(), "capacity") {
		t.Errorf("expected error about demands exceeding capacity, got: %v", err)
	}
}

func TestScheduler_PoolUnknownPoolRejected(t *testing.T) {
	t.Parallel()

	test1 := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"unknown": 1})

	_, err := NewScheduler([]*ExtensionTestSpec{test1},
		WithResourcePoolCapacity(map[string]int{"res": 3}))
	if err == nil {
		t.Fatal("expected error for unknown pool")
	}
	if !strings.Contains(err.Error(), "no capacity defined") {
		t.Errorf("expected error about undefined pool, got: %v", err)
	}
}

func TestScheduler_PoolNegativeDemandRejected(t *testing.T) {
	t.Parallel()

	test1 := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"res": -1})

	_, err := NewScheduler([]*ExtensionTestSpec{test1},
		WithResourcePoolCapacity(map[string]int{"res": 3}))
	if err == nil {
		t.Fatal("expected error for negative demand")
	}
	if !strings.Contains(err.Error(), "negative demand") {
		t.Errorf("expected error about negative demand, got: %v", err)
	}
}

func TestScheduler_PoolNegativeCapacityRejected(t *testing.T) {
	t.Parallel()

	test1 := newTestSpec("test1", Isolation{})

	_, err := NewScheduler([]*ExtensionTestSpec{test1},
		WithResourcePoolCapacity(map[string]int{"res": -1}))
	if err == nil {
		t.Fatal("expected error for negative capacity")
	}
	if !strings.Contains(err.Error(), "negative capacity") {
		t.Errorf("expected error about negative capacity, got: %v", err)
	}
}

func TestScheduler_PoolNoCapacityConfigured(t *testing.T) {
	t.Parallel()

	// Tests declare pools but no pool capacity is configured — pools are ignored.
	test1 := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"res": 5})
	test2 := newTestSpecWithResourcePools("test2", Isolation{}, map[string]int{"res": 5})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2})
	ctx := context.Background()

	first := scheduler.GetNextTestToRun(ctx)
	second := scheduler.GetNextTestToRun(ctx)

	if first == nil || second == nil {
		t.Fatal("without pool capacity configured, all tests should be immediately dispatchable")
	}
}

func TestScheduler_PoolConcurrentDispatchComplete(t *testing.T) {
	runner := newTrackingRunner()

	// 10 tests, each demanding 1 unit from a pool of 3.
	// With 5 workers, at most 3 should run at once.
	var specs []*ExtensionTestSpec
	for i := 0; i < 10; i++ {
		specs = append(specs, newTestSpecWithResourcePools(
			fmt.Sprintf("test%d", i), Isolation{}, map[string]int{"res": 1}))
	}

	scheduler := mustNewScheduler(t, specs,
		WithResourcePoolCapacity(map[string]int{"res": 3}))

	runTestsWithWorkers(context.Background(), scheduler, runner, 5)

	assertAllTestsCompleted(t, runner, 10)

	if peak := runner.peakConcurrency(); peak > 3 {
		t.Errorf("peak concurrency was %d, expected at most 3 (pool capacity)", peak)
	}

	// Verify snapshot shows clean state after all tests complete
	diag := scheduler.(SchedulerDiagnostics)
	snap := diag.GetSnapshot()
	if snap.QueueLength != 0 {
		t.Errorf("expected empty queue, got %d", snap.QueueLength)
	}
	if snap.ActiveCount != 0 {
		t.Errorf("expected 0 active, got %d", snap.ActiveCount)
	}
	if snap.ResourcePoolAvailable["res"] != 3 {
		t.Errorf("expected pool fully returned (3), got %d", snap.ResourcePoolAvailable["res"])
	}
}

func TestScheduler_GetSnapshot(t *testing.T) {
	t.Parallel()

	test1 := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"res": 2})
	test2 := newTestSpecWithResourcePools("test2", Isolation{}, map[string]int{"res": 1})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2},
		WithResourcePoolCapacity(map[string]int{"res": 3}))

	diag, ok := scheduler.(SchedulerDiagnostics)
	if !ok {
		t.Fatal("scheduler does not implement SchedulerDiagnostics")
	}

	// Initial snapshot
	snap := diag.GetSnapshot()
	if snap.QueueLength != 2 {
		t.Errorf("expected queue length 2, got %d", snap.QueueLength)
	}
	if snap.QueueFront != "test1" {
		t.Errorf("expected queue front test1, got %q", snap.QueueFront)
	}
	if snap.QueueFrontResourcePools["res"] != 2 {
		t.Errorf("expected queue front resource pools res=2, got %d", snap.QueueFrontResourcePools["res"])
	}
	if snap.ResourcePoolCapacity["res"] != 3 {
		t.Errorf("expected pool capacity 3, got %d", snap.ResourcePoolCapacity["res"])
	}
	if snap.ResourcePoolAvailable["res"] != 3 {
		t.Errorf("expected pool available 3, got %d", snap.ResourcePoolAvailable["res"])
	}
	if snap.ActiveCount != 0 {
		t.Errorf("expected 0 active, got %d", snap.ActiveCount)
	}

	// Dispatch test1
	ctx := context.Background()
	spec := scheduler.GetNextTestToRun(ctx)
	if spec == nil || spec.Name != "test1" {
		t.Fatalf("expected test1, got %v", spec)
	}

	snap = diag.GetSnapshot()
	if snap.QueueLength != 1 {
		t.Errorf("expected queue length 1, got %d", snap.QueueLength)
	}
	if snap.QueueFront != "test2" {
		t.Errorf("expected queue front test2, got %q", snap.QueueFront)
	}
	if snap.ResourcePoolAvailable["res"] != 1 {
		t.Errorf("expected pool available 1 (3-2), got %d", snap.ResourcePoolAvailable["res"])
	}
	if snap.ActiveCount != 1 {
		t.Errorf("expected 1 active, got %d", snap.ActiveCount)
	}

	// Complete test1
	scheduler.MarkTestComplete(spec)

	snap = diag.GetSnapshot()
	if snap.ResourcePoolAvailable["res"] != 3 {
		t.Errorf("expected pool available 3 (returned), got %d", snap.ResourcePoolAvailable["res"])
	}
	if snap.ActiveCount != 0 {
		t.Errorf("expected 0 active, got %d", snap.ActiveCount)
	}
}

func TestScheduler_GetSnapshotWithoutPools(t *testing.T) {
	t.Parallel()

	test1 := newTestSpec("test1", Isolation{})
	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1})

	diag := scheduler.(SchedulerDiagnostics)
	snap := diag.GetSnapshot()
	if snap.QueueLength != 1 {
		t.Errorf("expected queue length 1, got %d", snap.QueueLength)
	}
	if snap.ResourcePoolCapacity != nil {
		t.Error("expected nil ResourcePoolCapacity when no pools configured")
	}
	if snap.ResourcePoolAvailable != nil {
		t.Error("expected nil ResourcePoolAvailable when no pools configured")
	}
}

func TestScheduler_PoolOptionsThroughRun(t *testing.T) {
	t.Parallel()

	// Verify that SchedulerOption flows through specs.Run() to NewScheduler.
	// An invalid pool config should surface as an error from Run().
	specs := ExtensionTestSpecs{
		newTestSpecWithResourcePools("over-demand", Isolation{}, map[string]int{"res": 10}),
	}
	_, err := specs.Run(context.Background(), NullResultWriter{}, 1,
		WithResourcePoolCapacity(map[string]int{"res": 3}))
	if err == nil {
		t.Fatal("expected Run() to return error for over-demand pool config")
	}
}

func TestScheduler_WithSchedulerAccessor(t *testing.T) {
	t.Parallel()

	var captured Scheduler
	test1 := newTestSpec("test1", Isolation{})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1},
		WithSchedulerAccessor(func(s Scheduler) {
			captured = s
		}))

	if captured == nil {
		t.Fatal("accessor callback was not invoked")
	}
	if captured != scheduler {
		t.Error("accessor received different scheduler instance")
	}
}

func TestScheduler_SchedulerValidationBeforeBeforeAll(t *testing.T) {
	t.Parallel()

	var beforeAllRan bool
	specs := ExtensionTestSpecs{
		newTestSpecWithResourcePools("over-demand", Isolation{}, map[string]int{"res": 10}),
	}
	specs[0].Run = func(ctx context.Context) *ExtensionTestResult {
		return &ExtensionTestResult{Name: "over-demand", Result: ResultPassed}
	}
	specs.AddBeforeAll(func() { beforeAllRan = true })

	_, err := specs.Run(context.Background(), NullResultWriter{}, 1,
		WithResourcePoolCapacity(map[string]int{"res": 3}))
	if err == nil {
		t.Fatal("expected Run() to return error for over-demand pool config")
	}
	if beforeAllRan {
		t.Error("BeforeAll should not run when scheduler validation fails")
	}
}

func TestScheduler_ActiveCountUnderflowGuard(t *testing.T) {
	t.Parallel()

	spec := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"res": 1})
	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{spec},
		WithResourcePoolCapacity(map[string]int{"res": 2}))

	got := scheduler.GetNextTestToRun(context.Background())
	scheduler.MarkTestComplete(got)
	scheduler.MarkTestComplete(got) // double complete — should not underflow

	diag := scheduler.(SchedulerDiagnostics)
	snap := diag.GetSnapshot()
	if snap.ActiveCount != 0 {
		t.Errorf("expected activeCount == 0 after double-complete of single test, got %d", snap.ActiveCount)
	}
	if snap.ResourcePoolAvailable["res"] > snap.ResourcePoolCapacity["res"] {
		t.Errorf("pool over-returned: available %d > capacity %d",
			snap.ResourcePoolAvailable["res"], snap.ResourcePoolCapacity["res"])
	}
}

func TestScheduler_PoolDemandExceedsAvailableBlocksUntilCapacityReturned(t *testing.T) {
	t.Parallel()

	// Pool capacity is 3. Two tests: one demands 2, the other demands 2.
	// Both fit within total capacity (2 <= 3), but they can't run simultaneously
	// because 2+2=4 > 3. The second must wait until the first completes.
	test1 := newTestSpecWithResourcePools("needs2-a", Isolation{}, map[string]int{"res": 2})
	test2 := newTestSpecWithResourcePools("needs2-b", Isolation{}, map[string]int{"res": 2})

	runner := newTrackingRunner()
	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2},
		WithResourcePoolCapacity(map[string]int{"res": 3}))

	runTestsWithWorkers(context.Background(), scheduler, runner, 2)

	assertAllTestsCompleted(t, runner, 2)

	if peak := runner.peakConcurrency(); peak > 1 {
		t.Errorf("peak concurrency was %d, expected at most 1 (each test needs 2 of 3 pool units)", peak)
	}

	diag := scheduler.(SchedulerDiagnostics)
	snap := diag.GetSnapshot()
	if snap.ResourcePoolAvailable["res"] != 3 {
		t.Errorf("expected pool fully returned (3), got %d", snap.ResourcePoolAvailable["res"])
	}
}

func TestScheduler_PoolBlockedHeadSkipsToRunnableTest(t *testing.T) {
	t.Parallel()

	// Pre-dispatch a holder that consumes 2 of 3 capacity, leaving 1 available.
	// Queue: [big(3), small(1)]. big needs 3 but only 1 available — blocked.
	// Scheduler must skip big and dispatch small (needs 1, 1 >= 1).
	holder := newTestSpecWithResourcePools("holder", Isolation{}, map[string]int{"res": 2})
	big := newTestSpecWithResourcePools("big", Isolation{}, map[string]int{"res": 3})
	small := newTestSpecWithResourcePools("small", Isolation{}, map[string]int{"res": 1})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{holder, big, small},
		WithResourcePoolCapacity(map[string]int{"res": 3}))

	ctx := context.Background()

	// Dispatch holder — consumes 2 units, leaving 1 available
	got := scheduler.GetNextTestToRun(ctx)
	if got == nil || got.Name != "holder" {
		t.Fatalf("expected holder, got %v", got)
	}

	// Next dispatch: big(3) is at head but needs 3, only 1 available.
	// Scheduler must skip to small(1).
	got = scheduler.GetNextTestToRun(ctx)
	if got == nil || got.Name != "small" {
		t.Fatalf("expected scheduler to skip blocked big and dispatch small, got %v", got)
	}

	// Complete holder — returns 2 units, now 3 available. big can run.
	scheduler.MarkTestComplete(holder)
	scheduler.MarkTestComplete(small)
	got = scheduler.GetNextTestToRun(ctx)
	if got == nil || got.Name != "big" {
		t.Fatalf("expected big after capacity returned, got %v", got)
	}
}

func TestScheduler_PoolBlockedHeadDoesNotStarveQueue(t *testing.T) {
	t.Parallel()

	// Pre-dispatch holder consuming 1 of 2 capacity, leaving 1 available.
	// Queue: [big(2), small-a(1), small-b(1)].
	// big needs 2 but only 1 available — blocked at head.
	// Scheduler must skip big and dispatch small-a, then small-b blocks (0 left).
	// After holder and small-a complete, big runs.
	holder := newTestSpecWithResourcePools("holder", Isolation{}, map[string]int{"res": 1})
	big := newTestSpecWithResourcePools("big", Isolation{}, map[string]int{"res": 2})
	smallA := newTestSpecWithResourcePools("small-a", Isolation{}, map[string]int{"res": 1})
	smallB := newTestSpecWithResourcePools("small-b", Isolation{}, map[string]int{"res": 1})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{holder, big, smallA, smallB},
		WithResourcePoolCapacity(map[string]int{"res": 2}))

	ctx := context.Background()

	// Dispatch holder — consumes 1 unit, 1 available
	got := scheduler.GetNextTestToRun(ctx)
	if got == nil || got.Name != "holder" {
		t.Fatalf("expected holder, got %v", got)
	}

	// big(2) is head but blocked (only 1 available). Skip to small-a(1).
	got = scheduler.GetNextTestToRun(ctx)
	if got == nil || got.Name != "small-a" {
		t.Fatalf("expected small-a (skip blocked big), got %v", got)
	}

	// Now 0 available — complete holder to free 1 unit
	scheduler.MarkTestComplete(holder)

	// small-b(1) should now be dispatchable (1 available, big still needs 2)
	got = scheduler.GetNextTestToRun(ctx)
	if got == nil || got.Name != "small-b" {
		t.Fatalf("expected small-b (skip blocked big again), got %v", got)
	}

	// Complete both smalls — returns 2 units, big can finally run
	scheduler.MarkTestComplete(smallA)
	scheduler.MarkTestComplete(smallB)
	got = scheduler.GetNextTestToRun(ctx)
	if got == nil || got.Name != "big" {
		t.Fatalf("expected big after all capacity returned, got %v", got)
	}
}

func TestScheduler_ContextCancelWithMultipleBlockedWorkers(t *testing.T) {
	t.Parallel()

	// Pool capacity 1, 3 tests each needing 1 unit, 3 workers.
	// Only 1 test can run at a time. Cancel context mid-flight.
	// All workers must exit without deadlock.
	specs := make([]*ExtensionTestSpec, 3)
	for i := range specs {
		specs[i] = newTestSpecWithResourcePools(
			fmt.Sprintf("test%d", i), Isolation{}, map[string]int{"res": 1})
	}

	runner := newTrackingRunner()
	scheduler := mustNewScheduler(t, specs,
		WithResourcePoolCapacity(map[string]int{"res": 1}))

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay — enough for at least 1 test to dispatch
	go func() {
		time.Sleep(3 * schedulerTestDelay)
		cancel()
	}()

	runTestsWithWorkers(ctx, scheduler, runner, 3)

	// Some tests may have completed, but the key assertion is no deadlock:
	// if we reach this line, all 3 workers exited cleanly.
	completed := len(runner.getTestsRun())
	if completed < 1 {
		t.Error("expected at least 1 test to complete before cancellation")
	}
	if completed > 3 {
		t.Errorf("completed %d tests but only 3 exist", completed)
	}
}

func TestScheduler_PoolCapacityOneSerializesAllTests(t *testing.T) {
	t.Parallel()

	// Pool capacity=1, 5 tests each needing 1 unit, 3 workers.
	// Every test must wait for the previous to complete — strict serialization.
	specs := make([]*ExtensionTestSpec, 5)
	for i := range specs {
		specs[i] = newTestSpecWithResourcePools(
			fmt.Sprintf("test%d", i), Isolation{}, map[string]int{"res": 1})
	}

	runner := newTrackingRunner()
	scheduler := mustNewScheduler(t, specs,
		WithResourcePoolCapacity(map[string]int{"res": 1}))

	runTestsWithWorkers(context.Background(), scheduler, runner, 3)

	assertAllTestsCompleted(t, runner, 5)

	if peak := runner.peakConcurrency(); peak > 1 {
		t.Errorf("peak concurrency was %d, expected 1 (pool capacity=1 forces serialization)", peak)
	}

	diag := scheduler.(SchedulerDiagnostics)
	snap := diag.GetSnapshot()
	if snap.ResourcePoolAvailable["res"] != 1 {
		t.Errorf("expected pool fully returned (1), got %d", snap.ResourcePoolAvailable["res"])
	}
}

func TestScheduler_PoolZeroDemandDispatches(t *testing.T) {
	t.Parallel()

	// A test declaring zero demand for a pool should pass validation and dispatch normally.
	test1 := newTestSpecWithResourcePools("zero-demand", Isolation{}, map[string]int{"res": 0})
	test2 := newTestSpecWithResourcePools("normal", Isolation{}, map[string]int{"res": 1})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2},
		WithResourcePoolCapacity(map[string]int{"res": 3}))

	ctx := context.Background()
	first := scheduler.GetNextTestToRun(ctx)
	second := scheduler.GetNextTestToRun(ctx)

	if first == nil || second == nil {
		t.Fatal("both tests should be dispatchable")
	}
}

func TestScheduler_PoolOverflowCapExactValue(t *testing.T) {
	t.Parallel()

	// Double-complete should cap poolAvailable at exactly poolCapacity, not above.
	test1 := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"res": 2})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1},
		WithResourcePoolCapacity(map[string]int{"res": 3}))

	ctx := context.Background()
	spec := scheduler.GetNextTestToRun(ctx)
	if spec == nil {
		t.Fatal("test should be dispatchable")
	}

	scheduler.MarkTestComplete(spec)
	scheduler.MarkTestComplete(spec) // double-complete

	diag := scheduler.(SchedulerDiagnostics)
	snap := diag.GetSnapshot()
	if snap.ResourcePoolAvailable["res"] != snap.ResourcePoolCapacity["res"] {
		t.Errorf("after double-complete, available should equal capacity (%d), got %d",
			snap.ResourcePoolCapacity["res"], snap.ResourcePoolAvailable["res"])
	}
	if snap.ActiveCount != 0 {
		t.Errorf("after double-complete, activeCount should be 0, got %d", snap.ActiveCount)
	}
}

func TestScheduler_MarkTestCompleteNeverDispatched(t *testing.T) {
	t.Parallel()

	// Calling MarkTestComplete on a spec that was never dispatched should not panic.
	test1 := newTestSpec("dispatched", Isolation{
		Conflict: []string{conflictDatabase},
	})
	test2 := newTestSpec("never-dispatched", Isolation{
		Conflict: []string{conflictNetwork},
	})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2})

	ctx := context.Background()
	dispatched := scheduler.GetNextTestToRun(ctx)
	if dispatched == nil {
		t.Fatal("first test should be dispatchable")
	}

	// Complete the never-dispatched spec — should not panic
	scheduler.MarkTestComplete(test2)

	// Original dispatched test should still complete normally
	scheduler.MarkTestComplete(dispatched)

	diag := scheduler.(SchedulerDiagnostics)
	snap := diag.GetSnapshot()
	if snap.ActiveCount < 0 {
		t.Errorf("activeCount should not go negative, got %d", snap.ActiveCount)
	}
}

func TestScheduler_PoolZeroCapacityRejectsDemand(t *testing.T) {
	t.Parallel()

	// A pool with capacity 0 should reject any test demanding > 0 units.
	test1 := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"res": 1})

	_, err := NewScheduler([]*ExtensionTestSpec{test1},
		WithResourcePoolCapacity(map[string]int{"res": 0}))
	if err == nil {
		t.Fatal("expected error for demand exceeding zero-capacity pool")
	}
	if !strings.Contains(err.Error(), "demands") || !strings.Contains(err.Error(), "capacity") {
		t.Errorf("expected error about demands exceeding capacity, got: %v", err)
	}
}

func TestScheduler_GoroutineCleanupAfterManyDispatches(t *testing.T) {
	t.Parallel()

	// Each GetNextTestToRun spawns a goroutine for context cancellation.
	// Verify goroutines are cleaned up after many dispatch cycles.
	specs := make([]*ExtensionTestSpec, 50)
	for i := range specs {
		specs[i] = newTestSpec(fmt.Sprintf("test%d", i), Isolation{})
	}

	scheduler := mustNewScheduler(t, specs)
	ctx := context.Background()

	for {
		spec := scheduler.GetNextTestToRun(ctx)
		if spec == nil {
			break
		}
		scheduler.MarkTestComplete(spec)
	}

	// Allow goroutines time to exit via the closed done channel
	time.Sleep(5 * time.Millisecond)

	// No deadlock and all tests dispatched is the primary assertion.
	// A goroutine leak would eventually cause resource exhaustion in long-running
	// processes, but we can't assert an exact count without flakiness.
	diag := scheduler.(SchedulerDiagnostics)
	snap := diag.GetSnapshot()
	if snap.QueueLength != 0 {
		t.Errorf("expected empty queue, got %d", snap.QueueLength)
	}
	if snap.ActiveCount != 0 {
		t.Errorf("expected 0 active, got %d", snap.ActiveCount)
	}
}

func TestScheduler_TaintCleanupAfterAllComplete(t *testing.T) {
	t.Parallel()

	// Two taint-emitting tests followed by an intolerant test.
	// After both taint tests complete, the intolerant test must run,
	// proving taints are fully cleaned up (not left at count=0 in the map).
	taint1 := newTestSpec("taint1", Isolation{Taint: []string{taintGPU}})
	taint2 := newTestSpec("taint2", Isolation{Taint: []string{taintGPU}})
	intolerant := newTestSpec("intolerant", Isolation{})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{taint1, taint2, intolerant})
	ctx := context.Background()

	// Dispatch and complete taint1
	got := scheduler.GetNextTestToRun(ctx)
	if got.Name != "taint1" {
		t.Fatalf("expected taint1, got %s", got.Name)
	}
	scheduler.MarkTestComplete(got)

	// Dispatch and complete taint2
	got = scheduler.GetNextTestToRun(ctx)
	if got.Name != "taint2" {
		t.Fatalf("expected taint2, got %s", got.Name)
	}
	scheduler.MarkTestComplete(got)

	// Intolerant test should now run — taint is fully cleaned up
	got = scheduler.GetNextTestToRun(ctx)
	if got == nil {
		t.Fatal("intolerant test should be dispatchable after all taints cleared")
	}
	if got.Name != "intolerant" {
		t.Fatalf("expected intolerant, got %s", got.Name)
	}
}

func TestScheduler_PoolMultiPoolPartialExhaustion(t *testing.T) {
	t.Parallel()

	// Test demands pool "a":1 and "b":2. Capacities: "a":5, "b":2.
	// First dispatch consumes all of pool "b". Second test also needs "b":2
	// and must block despite "a" having ample capacity.
	test1 := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"a": 1, "b": 2})
	test2 := newTestSpecWithResourcePools("test2", Isolation{}, map[string]int{"a": 1, "b": 2})

	runner := newTrackingRunner()
	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1, test2},
		WithResourcePoolCapacity(map[string]int{"a": 5, "b": 2}))

	runTestsWithWorkers(context.Background(), scheduler, runner, 2)

	assertAllTestsCompleted(t, runner, 2)

	// Tests must run serially — pool "b" can only serve one at a time
	if peak := runner.peakConcurrency(); peak > 1 {
		t.Errorf("peak concurrency was %d, expected 1 (pool 'b' exhausted by single test)", peak)
	}

	diag := scheduler.(SchedulerDiagnostics)
	snap := diag.GetSnapshot()
	if snap.ResourcePoolAvailable["a"] != 5 {
		t.Errorf("pool 'a' should be fully returned (5), got %d", snap.ResourcePoolAvailable["a"])
	}
	if snap.ResourcePoolAvailable["b"] != 2 {
		t.Errorf("pool 'b' should be fully returned (2), got %d", snap.ResourcePoolAvailable["b"])
	}
}

func TestScheduler_GetSnapshotEmptyQueue(t *testing.T) {
	t.Parallel()

	test1 := newTestSpec("test1", Isolation{})
	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1})

	ctx := context.Background()
	got := scheduler.GetNextTestToRun(ctx)
	if got == nil {
		t.Fatal("test should be dispatchable")
	}
	scheduler.MarkTestComplete(got)

	// Queue is now drained — snapshot should reflect empty state
	diag := scheduler.(SchedulerDiagnostics)
	snap := diag.GetSnapshot()
	if snap.QueueLength != 0 {
		t.Errorf("expected queue length 0, got %d", snap.QueueLength)
	}
	if snap.QueueFront != "" {
		t.Errorf("expected empty QueueFront, got %q", snap.QueueFront)
	}
	if snap.QueueFrontResourcePools != nil {
		t.Errorf("expected nil QueueFrontResourcePools, got %v", snap.QueueFrontResourcePools)
	}
}

func TestScheduler_PoolMultiPoolDoubleCompleteOverflowCap(t *testing.T) {
	t.Parallel()

	// Test demands from two pools. Double-complete should cap each pool independently.
	test1 := newTestSpecWithResourcePools("test1", Isolation{}, map[string]int{"a": 2, "b": 1})

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{test1},
		WithResourcePoolCapacity(map[string]int{"a": 3, "b": 2}))

	ctx := context.Background()
	spec := scheduler.GetNextTestToRun(ctx)
	if spec == nil {
		t.Fatal("test should be dispatchable")
	}

	scheduler.MarkTestComplete(spec)
	scheduler.MarkTestComplete(spec) // double-complete

	diag := scheduler.(SchedulerDiagnostics)
	snap := diag.GetSnapshot()
	if snap.ResourcePoolAvailable["a"] != 3 {
		t.Errorf("pool 'a' should be capped at capacity (3), got %d", snap.ResourcePoolAvailable["a"])
	}
	if snap.ResourcePoolAvailable["b"] != 2 {
		t.Errorf("pool 'b' should be capped at capacity (2), got %d", snap.ResourcePoolAvailable["b"])
	}
}

func TestScheduler_DiagnosticsTypeAssertion(t *testing.T) {
	t.Parallel()

	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{newTestSpec("test1", Isolation{})})

	if _, ok := scheduler.(SchedulerDiagnostics); !ok {
		t.Error("scheduler should implement SchedulerDiagnostics")
	}
}

func TestScheduler_MarkTestCompleteIgnoresPostDispatchIsolationMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*ExtensionTestSpec)
	}{
		{
			name: "nil out conflict slice",
			mutate: func(spec *ExtensionTestSpec) {
				spec.Resources.Isolation.Conflict = nil
			},
		},
		{
			name: "rewrite conflict in place",
			mutate: func(spec *ExtensionTestSpec) {
				spec.Resources.Isolation.Conflict[0] = "mutated"
			},
		},
		{
			name: "truncate conflict slice",
			mutate: func(spec *ExtensionTestSpec) {
				spec.Resources.Isolation.Conflict = spec.Resources.Isolation.Conflict[:0]
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first := newTestSpec("first", Isolation{Conflict: []string{conflictDatabase}})
			second := newTestSpec("second", Isolation{Conflict: []string{conflictDatabase}})
			scheduler := mustNewScheduler(t, []*ExtensionTestSpec{first, second})

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			got := scheduler.GetNextTestToRun(ctx)
			if got == nil || got.Name != "first" {
				t.Fatalf("expected first spec, got %v", got)
			}

			tt.mutate(first)
			scheduler.MarkTestComplete(first)

			got = scheduler.GetNextTestToRun(ctx)
			if got == nil || got.Name != "second" {
				t.Fatalf("second spec should run after reservation is released, got %v ctx=%v", got, ctx.Err())
			}
		})
	}
}

func TestScheduler_MarkTestCompleteIgnoresPostDispatchTaintMutation(t *testing.T) {
	t.Parallel()

	first := newTestSpec("first", Isolation{Taint: []string{taintGPU}})
	second := newTestSpec("second", Isolation{})
	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{first, second})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got := scheduler.GetNextTestToRun(ctx)
	if got == nil || got.Name != "first" {
		t.Fatalf("expected first spec, got %v", got)
	}

	first.Resources.Isolation.Taint = nil
	scheduler.MarkTestComplete(first)

	got = scheduler.GetNextTestToRun(ctx)
	if got == nil || got.Name != "second" {
		t.Fatalf("second spec should run after taint reservation is released, got %v ctx=%v", got, ctx.Err())
	}
}

func TestScheduler_MarkTestCompleteIgnoresPostDispatchPoolMutation(t *testing.T) {
	t.Parallel()

	first := newTestSpecWithResourcePools("first", Isolation{}, map[string]int{"res": 1})
	second := newTestSpecWithResourcePools("second", Isolation{}, map[string]int{"res": 1})
	scheduler := mustNewScheduler(t, []*ExtensionTestSpec{first, second},
		WithResourcePoolCapacity(map[string]int{"res": 1}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got := scheduler.GetNextTestToRun(ctx)
	if got == nil || got.Name != "first" {
		t.Fatalf("expected first spec, got %v", got)
	}

	first.Resources.ResourcePools = nil
	scheduler.MarkTestComplete(first)

	got = scheduler.GetNextTestToRun(ctx)
	if got == nil || got.Name != "second" {
		t.Fatalf("second spec should run after pool units are returned, got %v ctx=%v", got, ctx.Err())
	}
}

func TestScheduler_DuplicateSpecPointerRejected(t *testing.T) {
	t.Parallel()

	duplicate := newTestSpec("duplicate", Isolation{})
	_, err := NewScheduler([]*ExtensionTestSpec{duplicate, duplicate})
	if err == nil {
		t.Fatal("expected duplicate spec pointer to be rejected")
	}
	if !strings.Contains(err.Error(), "same spec pointer more than once") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestScheduler_NilSpecRejected(t *testing.T) {
	t.Parallel()

	_, err := NewScheduler([]*ExtensionTestSpec{nil})
	if err == nil || !strings.Contains(err.Error(), "must not be nil") {
		t.Fatalf("expected nil spec error, got %v", err)
	}
}
