package extensiontests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift-eng/openshift-tests-extension/pkg/flags"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"
	"github.com/stretchr/testify/assert"

	"github.com/openshift-eng/openshift-tests-extension/pkg/dbtime"
)

func TestExtensionTestSpecs_Walk(t *testing.T) {
	specs := ExtensionTestSpecs{
		{Name: "test1"},
		{Name: "test2"},
	}

	var walkedNames []string
	specs.Walk(func(spec *ExtensionTestSpec) {
		walkedNames = append(walkedNames, spec.Name)
	})

	assert.Equal(t, []string{"test1", "test2"}, walkedNames)
}

func TestExtensionTestSpecs_MustFilter(t *testing.T) {
	specs := ExtensionTestSpecs{
		{Name: "test1"},
	}

	defer func() {
		if r := recover(); r != nil {
			assert.Contains(t, r.(string), "filter did not succeed")
		}
	}()

	// CEL expression that should fail
	specs.MustFilter([]string{"invalid_expr"})
	t.Errorf("Expected panic, but code continued")
}

func TestExtensionTestSpecs_Filter(t *testing.T) {
	tests := []struct {
		name     string
		specs    ExtensionTestSpecs
		celExprs []string
		want     ExtensionTestSpecs
		wantErr  bool
	}{
		{
			name: "simple filter on name",
			specs: ExtensionTestSpecs{
				{
					Name: "test1",
				},
				{
					Name: "test2",
				},
			},
			celExprs: []string{`name == "test1"`},
			want: ExtensionTestSpecs{
				{
					Name: "test1",
				},
			},
		},
		{
			name: "filter on tags",
			specs: ExtensionTestSpecs{
				{Name: "test1", Tags: map[string]string{"env": "prod"}},
				{Name: "test2", Tags: map[string]string{"env": "dev"}},
			},
			celExprs: []string{"tags['env'] == 'prod'"},
			want: ExtensionTestSpecs{
				{Name: "test1", Tags: map[string]string{"env": "prod"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.specs.Filter(tt.celExprs)
			if (err != nil) != tt.wantErr {
				t.Errorf("Filter() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Filter() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtensionTestSpecs_AddLabel(t *testing.T) {
	specs := ExtensionTestSpecs{
		{Name: "test1", Labels: sets.New[string]()},
	}

	specs = specs.AddLabel("critical")
	assert.True(t, specs[0].Labels.Has("critical"))
}

func TestExtensionTestSpecs_RemoveLabel(t *testing.T) {
	specs := ExtensionTestSpecs{
		{Name: "test1", Labels: sets.New[string]("to_remove")},
	}
	specs = specs.RemoveLabel("to_remove")
	assert.False(t, specs[0].Labels.Has("to_remove"))
}

func TestExtensionTestSpecs_SetTag(t *testing.T) {
	specs := ExtensionTestSpecs{
		{Name: "test1", Tags: make(map[string]string)},
	}

	specs = specs.SetTag("priority", "high")
	assert.Equal(t, "high", specs[0].Tags["priority"])
}

func TestExtensionTestSpecs_UnsetTag(t *testing.T) {
	specs := ExtensionTestSpecs{
		{Name: "test1", Tags: map[string]string{"priority": "high"}},
	}

	specs = specs.UnsetTag("priority")
	_, exists := specs[0].Tags["priority"]
	assert.False(t, exists)
}

func produceTestResult(name string, duration time.Duration) *ExtensionTestResult {
	return &ExtensionTestResult{
		Name:      name,
		Duration:  duration.Milliseconds(),
		StartTime: dbtime.Ptr(time.Now().UTC().Add(-duration)),
		EndTime:   dbtime.Ptr(time.Now()),
		Result:    ResultPassed,
	}
}

func TestExtensionTestSpecs_Run_IsolationAware(t *testing.T) {
	runner := newTrackingRunner()

	specs := ExtensionTestSpecs{
		specWithRunTracking(runner, "test1", Isolation{Conflict: []string{conflictDatabase}}),
		specWithRunTracking(runner, "test2", Isolation{Conflict: []string{conflictDatabase}}),
		specWithRunTracking(runner, "test3", Isolation{Conflict: []string{conflictNetwork}}),
	}

	_, err := specs.Run(context.Background(), NullResultWriter{}, defaultSchedulerTestWorkers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertAllTestsCompleted(t, runner, 3)
	assertOverlap(t, runner, "test1", "test2", false, "Run() respects same conflict")
	assertOverlap(t, runner, "test1", "test3", true, "Run() allows different conflicts")
}

func TestExtensionTestSpecs_HookExecution(t *testing.T) {
	testCases := []struct {
		name                string
		expectedBeforeAll   int32
		expectedBeforeSpawn int32
		expectedBeforeEach  int32
		expectedAfterEach   int32
		expectedAfterAll    int32
		numSpecs            int
	}{
		{
			name:                "all hooks run - high test count",
			expectedBeforeAll:   1,
			expectedBeforeSpawn: 10000,
			expectedBeforeEach:  10000,
			expectedAfterEach:   10000,
			expectedAfterAll:    1,
			numSpecs:            10000,
		},
		{
			name:                "no AddBeforeAll",
			expectedBeforeAll:   0,
			expectedBeforeSpawn: 2,
			expectedBeforeEach:  2,
			expectedAfterEach:   2,
			expectedAfterAll:    1,
			numSpecs:            2,
		},
		{
			name:                "no AddAfterEach",
			expectedBeforeAll:   1,
			expectedBeforeSpawn: 2,
			expectedBeforeEach:  2,
			expectedAfterEach:   0,
			expectedAfterAll:    1,
			numSpecs:            2,
		},
		{
			name:                "only AddAfterAll",
			expectedBeforeAll:   0,
			expectedBeforeSpawn: 0,
			expectedBeforeEach:  0,
			expectedAfterEach:   0,
			expectedAfterAll:    1,
			numSpecs:            2,
		},
		{
			name:                "beforeEach only",
			expectedBeforeAll:   0,
			expectedBeforeSpawn: 0,
			expectedBeforeEach:  2,
			expectedAfterEach:   0,
			expectedAfterAll:    0,
			numSpecs:            2,
		},
		{
			name:                "beforeSpawn only",
			expectedBeforeAll:   0,
			expectedBeforeSpawn: 2,
			expectedBeforeEach:  0,
			expectedAfterEach:   0,
			expectedAfterAll:    0,
			numSpecs:            2,
		},
		{
			name:                "beforeAll and afterAll only",
			expectedBeforeAll:   1,
			expectedBeforeSpawn: 0,
			expectedBeforeEach:  0,
			expectedAfterEach:   0,
			expectedAfterAll:    1,
			numSpecs:            2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			specs := ExtensionTestSpecs{}
			for i := 0; i < tc.numSpecs; i++ {
				spec := &ExtensionTestSpec{
					Name: fmt.Sprintf("test spec %d", i+1),
					Run: func(ctx context.Context) *ExtensionTestResult {
						return produceTestResult(fmt.Sprintf("test result %d", i+1), 20*time.Second)
					},
				}
				spec.RunParallel = spec.Run
				specs = append(specs, spec)
			}

			// Hook invocation counters
			var beforeAllCount, beforeSpawnCount, beforeEachCount, afterEachCount, afterAllCount atomic.Int32

			// Set up hooks based on the expected test case
			if tc.expectedBeforeAll > 0 {
				specs.AddBeforeAll(func() {
					beforeAllCount.Add(1)
				})
			}
			if tc.expectedBeforeSpawn > 0 {
				specs.AddBeforeSpawn(func(_ string, _ *SpawnOptions) {
					beforeSpawnCount.Add(1)
				})
			}
			if tc.expectedBeforeEach > 0 {
				specs.AddBeforeEach(func(_ ExtensionTestSpec) {
					beforeEachCount.Add(1)
				})
			}
			if tc.expectedAfterEach > 0 {
				specs.AddAfterEach(func(_ *ExtensionTestResult) {
					afterEachCount.Add(1)
				})
			}
			if tc.expectedAfterAll > 0 {
				specs.AddAfterAll(func() {
					afterAllCount.Add(1)
				})
			}

			// Run the test specs
			_, err := specs.Run(context.TODO(), NullResultWriter{}, 10)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Verify the hook invocation counts
			if beforeAllCount.Load() != tc.expectedBeforeAll {
				t.Errorf("Expected BeforeAll to run %d times, but ran %d times", tc.expectedBeforeAll,
					beforeAllCount.Load())
			}
			if beforeSpawnCount.Load() != tc.expectedBeforeSpawn {
				t.Errorf("Expected BeforeSpawn to run %d times, but ran %d times", tc.expectedBeforeSpawn,
					beforeSpawnCount.Load())
			}
			if beforeEachCount.Load() != tc.expectedBeforeEach {
				t.Errorf("Expected BeforeEach to run %d times, but ran %d times", tc.expectedBeforeEach,
					beforeEachCount.Load())
			}
			if afterEachCount.Load() != tc.expectedAfterEach {
				t.Errorf("Expected AfterEach to run %d times, but ran %d times", tc.expectedAfterEach,
					afterEachCount.Load())
			}
			if afterAllCount.Load() != tc.expectedAfterAll {
				t.Errorf("Expected AfterAll to run %d times, but ran %d times", tc.expectedAfterAll,
					afterAllCount.Load())
			}
		})
	}
}

func TestExtensionTestSpecs_BeforeSpawnSetsEnv(t *testing.T) {
	var capturedEnv atomic.Value
	specs := ExtensionTestSpecs{
		parallelPassingSpec("env-test"),
		parallelPassingSpec("other"),
	}

	specs.AddBeforeSpawn(func(_ string, options *SpawnOptions) {
		options.Env = map[string]string{"INJECTED": "yes"}
	})

	specs.AddBeforeEach(func(spec ExtensionTestSpec) {
		if spec.Name == "env-test" {
			capturedEnv.Store(spec.Env)
		}
	})

	_, err := specs.Run(context.TODO(), NullResultWriter{}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	env, ok := capturedEnv.Load().(map[string]string)
	if !ok {
		t.Fatal("BeforeEach did not capture env")
	}
	if env["INJECTED"] != "yes" {
		t.Errorf("expected Env[INJECTED]=yes, got %v", env)
	}
}

func TestExtensionTestSpecs_BeforeSpawnRunsBeforeBeforeEach(t *testing.T) {
	var order []string
	var mu sync.Mutex

	specs := ExtensionTestSpecs{parallelPassingSpec("order-test"), parallelPassingSpec("other")}

	specs.AddBeforeSpawn(func(name string, _ *SpawnOptions) {
		if name != "order-test" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		order = append(order, "beforeSpawn")
	})

	specs.AddBeforeEach(func(spec ExtensionTestSpec) {
		if spec.Name != "order-test" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		order = append(order, "beforeEach")
	})

	_, err := specs.Run(context.TODO(), NullResultWriter{}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assert.Equal(t, []string{"beforeSpawn", "beforeEach"}, order)
}

func TestExtensionTestSpecs_BeforeSpawnOptionsVisibleInRunParallel(t *testing.T) {
	type capturedOptions struct {
		env       map[string]string
		timeout   time.Duration
		resources Resources
	}
	var captured sync.Map

	makeSpec := func(name string) *ExtensionTestSpec {
		spec := &ExtensionTestSpec{
			Name: name,
			Run: func(ctx context.Context) *ExtensionTestResult {
				t.Errorf("Run() should not be called for multi-spec suite")
				return &ExtensionTestResult{Name: name, Result: ResultFailed}
			},
		}
		spec.RunParallel = func(ctx context.Context) *ExtensionTestResult {
			captured.Store(name, capturedOptions{
				env:       spec.Env,
				timeout:   spec.Timeout,
				resources: spec.Resources,
			})
			return &ExtensionTestResult{Name: name, Result: ResultPassed}
		}
		return spec
	}

	specs := ExtensionTestSpecs{makeSpec("spec-a"), makeSpec("spec-b")}
	for _, spec := range specs {
		spec.Resources = Resources{
			Isolation:     Isolation{Conflict: []string{"shared"}},
			ResourcePools: map[string]int{"workers": 1},
		}
	}

	specs.AddBeforeSpawn(func(name string, options *SpawnOptions) {
		options.Env = map[string]string{"ASSIGNED_TO": name}
		options.Timeout = time.Minute
	})

	_, err := specs.Run(
		context.TODO(),
		NullResultWriter{},
		2,
		WithResourcePoolCapacity(map[string]int{"workers": 2}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, name := range []string{"spec-a", "spec-b"} {
		val, ok := captured.Load(name)
		if !ok {
			t.Fatalf("RunParallel never ran for %s", name)
		}
		got := val.(capturedOptions)
		if got.env["ASSIGNED_TO"] != name {
			t.Errorf("spec %s: expected Env[ASSIGNED_TO]=%s, got %v", name, name, got.env)
		}
		if got.timeout != time.Minute {
			t.Errorf("spec %s: expected one-minute timeout, got %v", name, got.timeout)
		}
		assert.Equal(t, Resources{
			Isolation:     Isolation{Conflict: []string{"shared"}},
			ResourcePools: map[string]int{"workers": 1},
		}, got.resources)
	}
}

func TestExtensionTestSpecs_BeforeEachCannotMutateEnvSeenByRunParallel(t *testing.T) {
	var capturedEnv atomic.Value

	spec := &ExtensionTestSpec{
		Name: "env-isolation",
		Run: func(ctx context.Context) *ExtensionTestResult {
			t.Error("Run() should not be called for multi-spec suite")
			return &ExtensionTestResult{Name: "env-isolation", Result: ResultFailed}
		},
	}
	spec.RunParallel = func(ctx context.Context) *ExtensionTestResult {
		capturedEnv.Store(spec.Env)
		return &ExtensionTestResult{Name: spec.Name, Result: ResultPassed}
	}
	// A second spec is required so runSpec takes the RunParallel path.
	other := &ExtensionTestSpec{
		Name: "other",
		Run:  spec.Run,
	}
	other.RunParallel = func(ctx context.Context) *ExtensionTestResult {
		return &ExtensionTestResult{Name: other.Name, Result: ResultPassed}
	}

	specs := ExtensionTestSpecs{spec, other}
	specs.AddBeforeSpawn(func(name string, options *SpawnOptions) {
		options.Env = map[string]string{"ASSIGNED_TO": name}
	})
	specs.AddBeforeEach(func(s ExtensionTestSpec) {
		s.Env["ASSIGNED_TO"] = "tampered"
		s.Env["EXTRA"] = "from-before-each"
	})

	_, err := specs.Run(context.TODO(), NullResultWriter{}, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	env, ok := capturedEnv.Load().(map[string]string)
	if !ok {
		t.Fatal("RunParallel did not capture env")
	}
	if env["ASSIGNED_TO"] != "env-isolation" {
		t.Errorf("expected Env[ASSIGNED_TO]=env-isolation, got %v", env)
	}
	if _, present := env["EXTRA"]; present {
		t.Errorf("BeforeEach must not add keys to the Env seen by RunParallel, got %v", env)
	}
}

func passingSpec(name string) *ExtensionTestSpec {
	return &ExtensionTestSpec{
		Name: name,
		Run: func(ctx context.Context) *ExtensionTestResult {
			return &ExtensionTestResult{Name: name, Result: ResultPassed}
		},
	}
}

func parallelPassingSpec(name string) *ExtensionTestSpec {
	spec := passingSpec(name)
	spec.RunParallel = spec.Run
	return spec
}

func TestExtensionTestSpecs_MultipleBeforeSpawnRunInRegistrationOrder(t *testing.T) {
	var order []string
	var mu sync.Mutex

	specs := ExtensionTestSpecs{parallelPassingSpec("order-test"), parallelPassingSpec("other")}
	specs.AddBeforeSpawn(func(name string, options *SpawnOptions) {
		if name != "order-test" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		order = append(order, "first")
		options.Env = map[string]string{"FIRST": "1"}
	})
	specs.AddBeforeSpawn(func(name string, options *SpawnOptions) {
		if name != "order-test" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		order = append(order, "second")
		options.Env["SECOND"] = "2"
	})

	var capturedEnv atomic.Value
	specs.AddBeforeEach(func(spec ExtensionTestSpec) {
		if spec.Name == "order-test" {
			capturedEnv.Store(spec.Env)
		}
	})

	_, err := specs.Run(context.TODO(), NullResultWriter{}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assert.Equal(t, []string{"first", "second"}, order)
	env, ok := capturedEnv.Load().(map[string]string)
	if !ok {
		t.Fatal("BeforeEach did not capture env")
	}
	if env["FIRST"] != "1" || env["SECOND"] != "2" {
		t.Errorf("expected both hooks to contribute Env, got %v", env)
	}
}

func TestExtensionTestSpecs_BeforeSpawnSkippedInSpawnedChild(t *testing.T) {
	t.Setenv(SpawnedChildEnv, "1")

	var ran atomic.Int32
	specs := ExtensionTestSpecs{parallelPassingSpec("child-skip"), parallelPassingSpec("other")}
	specs.AddBeforeSpawn(func(_ string, _ *SpawnOptions) {
		ran.Add(1)
	})

	_, err := specs.Run(context.TODO(), NullResultWriter{}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ran.Load() != 0 {
		t.Errorf("BeforeSpawn must not run in a spawned run-test child, ran %d times", ran.Load())
	}
}

func TestExtensionTestSpecs_BeforeSpawnSkippedWithoutParallelRun(t *testing.T) {
	var ran atomic.Int32
	specs := ExtensionTestSpecs{parallelPassingSpec("direct-run-test")}
	specs.AddBeforeSpawn(func(_ string, _ *SpawnOptions) {
		ran.Add(1)
	})

	_, err := specs.Run(context.TODO(), NullResultWriter{}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ran.Load() != 0 {
		t.Errorf("BeforeSpawn must not run when the spec executes in-process, ran %d times", ran.Load())
	}
}

const (
	customRunnerChildEnv = "OTE_TEST_CUSTOM_RUNNER_CHILD"
	customRunnerLogEnv   = "OTE_TEST_CUSTOM_RUNNER_LOG"
)

func TestExtensionTestSpecs_CustomSubprocessRunsBeforeSpawnOnlyInParent(t *testing.T) {
	logPaths := map[string]string{
		"first":  t.TempDir() + "/first.log",
		"second": t.TempDir() + "/second.log",
	}
	hookErrs := make(chan error, len(logPaths))
	makeSpec := func(name string) *ExtensionTestSpec {
		spec := passingSpec(name)
		logPath := logPaths[name]
		spec.RunParallel = func(ctx context.Context) *ExtensionTestResult {
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExtensionTestSpecs_CustomSubprocessChild$")
			cmd.Env = append(
				os.Environ(),
				customRunnerChildEnv+"=1",
				customRunnerLogEnv+"="+logPath,
				SpawnedChildEnv+"=1",
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				return &ExtensionTestResult{Name: name, Result: ResultFailed, Error: fmt.Sprintf("%v: %s", err, output)}
			}
			return &ExtensionTestResult{Name: name, Result: ResultPassed}
		}
		return spec
	}

	specs := ExtensionTestSpecs{makeSpec("first"), makeSpec("second")}
	specs.AddBeforeSpawn(func(name string, _ *SpawnOptions) {
		if err := appendHookEvent(logPaths[name], "parent"); err != nil {
			hookErrs <- err
		}
	})

	if _, err := specs.Run(context.Background(), NullResultWriter{}, 2); err != nil {
		t.Fatalf("custom subprocess run failed: %v", err)
	}
	close(hookErrs)
	for err := range hookErrs {
		t.Errorf("recording parent hook: %v", err)
	}
	for name, logPath := range logPaths {
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read %s hook log: %v", name, err)
		}
		if got := strings.Fields(string(data)); !reflect.DeepEqual(got, []string{"parent"}) {
			t.Errorf("%s hook events: got %v, want [parent]", name, got)
		}
	}
}

func TestExtensionTestSpecs_CustomSubprocessChild(t *testing.T) {
	if os.Getenv(customRunnerChildEnv) != "1" {
		t.Skip("custom subprocess helper")
	}

	// Multiple specs force the RunParallel path, proving SpawnedChildEnv rather
	// than single-spec execution prevents BeforeSpawn from running in the child.
	specs := ExtensionTestSpecs{
		parallelPassingSpec("child-first"),
		parallelPassingSpec("child-second"),
	}
	specs.AddBeforeSpawn(func(_ string, _ *SpawnOptions) {
		if err := appendHookEvent(os.Getenv(customRunnerLogEnv), "child"); err != nil {
			t.Errorf("recording child hook: %v", err)
		}
	})
	if _, err := specs.Run(context.Background(), NullResultWriter{}, 2); err != nil {
		t.Fatalf("child run failed: %v", err)
	}
}

func appendHookEvent(path, event string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintln(f, event)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func TestExtensionTestSpecs_RunRejectsDuplicateSpecPointerBeforeHooks(t *testing.T) {
	spec := parallelPassingSpec("duplicate")
	specs := ExtensionTestSpecs{spec, spec}
	var beforeSpawnCalls atomic.Int32
	specs.AddBeforeSpawn(func(_ string, _ *SpawnOptions) {
		beforeSpawnCalls.Add(1)
	})

	_, err := specs.Run(context.Background(), NullResultWriter{}, 2)
	if err == nil || !strings.Contains(err.Error(), "same spec pointer more than once") {
		t.Fatalf("expected duplicate spec error, got %v", err)
	}
	if beforeSpawnCalls.Load() != 0 {
		t.Fatalf("BeforeSpawn ran before duplicate validation: %d calls", beforeSpawnCalls.Load())
	}
}

func TestExtensionTestSpec_EnvOmittedFromJSON(t *testing.T) {
	spec := ExtensionTestSpec{
		Name: "json-omit",
		Env:  map[string]string{"SECRET": "should-not-appear"},
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	encoded := string(data)
	if strings.Contains(encoded, "SECRET") || strings.Contains(encoded, "should-not-appear") {
		t.Errorf("Env must be omitted from JSON, got %s", encoded)
	}
}

func TestExtensionTestSpec_Include(t *testing.T) {
	testCases := []struct {
		name     string
		cel      string
		spec     *ExtensionTestSpec
		expected *ExtensionTestSpec
	}{
		{
			name: "simple OR expression",
			cel:  Or(PlatformEquals("aws"), NetworkEquals("ovn")),
			spec: &ExtensionTestSpec{},
			expected: &ExtensionTestSpec{
				EnvironmentSelector: EnvironmentSelector{
					Include: `(platform=="aws" || network=="ovn")`},
			},
		},
		{
			name: "simple AND expression",
			cel:  And(UpgradeEquals("minor"), TopologyEquals("microshift"), ArchitectureEquals("amd64")),
			spec: &ExtensionTestSpec{},
			expected: &ExtensionTestSpec{
				EnvironmentSelector: EnvironmentSelector{
					Include: `(upgrade=="minor" && topology=="microshift" && architecture=="amd64")`},
			},
		},
		{
			name: "complex expression with AND and OR",
			cel:  And(Or(PlatformEquals("aws"), NetworkEquals("ovn")), And(UpgradeEquals("minor"), TopologyEquals("microshift"), ArchitectureEquals("amd64"))),
			spec: &ExtensionTestSpec{},
			expected: &ExtensionTestSpec{
				EnvironmentSelector: EnvironmentSelector{
					Include: `((platform=="aws" || network=="ovn") && (upgrade=="minor" && topology=="microshift" && architecture=="amd64"))`},
			},
		},
		{
			name: "include already exists; is ORed",
			cel:  Or(PlatformEquals("aws"), NetworkEquals("ovn")),
			spec: &ExtensionTestSpec{
				EnvironmentSelector: EnvironmentSelector{
					Include: `(platform=="gce")`,
				},
			},
			expected: &ExtensionTestSpec{
				EnvironmentSelector: EnvironmentSelector{
					Include: `((platform=="gce")) || ((platform=="aws" || network=="ovn"))`},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resultingSpec := tc.spec.Include(tc.cel)
			if diff := cmp.Diff(tc.expected, resultingSpec, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("Include returned unexpected resulting spec (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExtensionTestSpec_Exclude(t *testing.T) {
	testCases := []struct {
		name     string
		cel      string
		spec     *ExtensionTestSpec
		expected *ExtensionTestSpec
	}{
		{
			name: "simple OR expression",
			cel:  Or(InstallerEquals("upi"), VersionEquals("4.19")),
			spec: &ExtensionTestSpec{},
			expected: &ExtensionTestSpec{
				EnvironmentSelector: EnvironmentSelector{
					Exclude: `(installer=="upi" || version=="4.19")`},
			},
		},
		{
			name: "complex expression utilizing facts",
			cel:  And(FactEquals("cool.component", "absolutely"), FactEquals("simple.to.use", "true")),
			spec: &ExtensionTestSpec{},
			expected: &ExtensionTestSpec{
				EnvironmentSelector: EnvironmentSelector{
					Exclude: `((fact_keys.exists(k, k=="cool.component") && facts["cool.component"].matches("absolutely")) && (fact_keys.exists(k, k=="simple.to.use") && facts["simple.to.use"].matches("true")))`},
			},
		},
		{
			name: "exclude already exists; is ORed",
			cel:  Or(PlatformEquals("aws"), NetworkEquals("ovn")),
			spec: &ExtensionTestSpec{
				EnvironmentSelector: EnvironmentSelector{
					Exclude: `(platform=="gce")`,
				},
			},
			expected: &ExtensionTestSpec{
				EnvironmentSelector: EnvironmentSelector{
					Exclude: `((platform=="gce")) || ((platform=="aws" || network=="ovn"))`},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resultingSpec := tc.spec.Exclude(tc.cel)
			if diff := cmp.Diff(tc.expected, resultingSpec, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("Include returned unexpected resulting spec (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExtensionTestSpecs_FilterByEnvironment(t *testing.T) {
	testCases := []struct {
		name     string
		specs    ExtensionTestSpecs
		envFlags flags.EnvironmentalFlags
		want     ExtensionTestSpecs
		wantErr  error
	}{
		{
			name: "no environment info",
			specs: ExtensionTestSpecs{
				{
					Name: "spec1",
				},
				{
					Name: "spec2",
				},
			},
			envFlags: flags.EnvironmentalFlags{Platform: "aws"},
			want: ExtensionTestSpecs{
				{
					Name: "spec1",
				},
				{
					Name: "spec2",
				},
			},
		},
		{
			name: "filter on single include expression",
			specs: ExtensionTestSpecs{
				{
					Name: "spec-aws-only",
					EnvironmentSelector: EnvironmentSelector{
						Include: PlatformEquals("aws"),
					},
				},
				{
					Name: "spec-gcp-only",
					EnvironmentSelector: EnvironmentSelector{
						Include: PlatformEquals("gcp"),
					},
				},
			},
			envFlags: flags.EnvironmentalFlags{Platform: "aws"},
			want: ExtensionTestSpecs{
				{
					Name: "spec-aws-only",
					EnvironmentSelector: EnvironmentSelector{
						Include: PlatformEquals("aws"),
					},
				},
			},
		},
		{
			name: "filter on single exclude expression",
			specs: ExtensionTestSpecs{
				{
					Name: "spec-non-aws-only",
					EnvironmentSelector: EnvironmentSelector{
						Exclude: PlatformEquals("aws"),
					},
				},
				{
					Name: "spec-non-gcp-only",
					EnvironmentSelector: EnvironmentSelector{
						Exclude: PlatformEquals("gcp"),
					},
				},
			},
			envFlags: flags.EnvironmentalFlags{Platform: "aws"},
			want: ExtensionTestSpecs{
				{
					Name: "spec-non-gcp-only",
					EnvironmentSelector: EnvironmentSelector{
						Exclude: PlatformEquals("gcp"),
					},
				},
			},
		},
		{
			name: "filter on complex expressions",
			specs: ExtensionTestSpecs{
				{
					Name: "complex-spec-included",
					EnvironmentSelector: EnvironmentSelector{
						Include: And(
							Or(
								PlatformEquals("aws"), NetworkEquals("ovn"), NetworkStackEquals("ipv6"), ExternalConnectivityEquals("Disconnected")),
							And(
								UpgradeEquals("minor"), TopologyEquals("microshift"), ArchitectureEquals("amd64"),
							),
						),
					},
				},
				{
					Name: "complex-spec-excluded",
					EnvironmentSelector: EnvironmentSelector{
						Exclude: And(
							Or(
								PlatformEquals("aws"), NetworkEquals("ovn"), NetworkStackEquals("ipv6")),
							And(
								UpgradeEquals("minor"), TopologyEquals("microshift"), ArchitectureEquals("amd64"),
							),
						),
					},
				},
			},
			envFlags: flags.EnvironmentalFlags{
				Platform:             "aws",
				Network:              "sdn",
				NetworkStack:         "ipv6",
				Upgrade:              "minor",
				Topology:             "microshift",
				Architecture:         "amd64",
				Version:              "4.18",
				ExternalConnectivity: "Disconnected",
			},
			want: ExtensionTestSpecs{
				{
					Name: "complex-spec-included",
					EnvironmentSelector: EnvironmentSelector{
						Include: And(
							Or(
								PlatformEquals("aws"), NetworkEquals("ovn"), NetworkStackEquals("ipv6"), ExternalConnectivityEquals("Disconnected")),
							And(
								UpgradeEquals("minor"), TopologyEquals("microshift"), ArchitectureEquals("amd64"),
							),
						),
					},
				},
			},
		},
		{
			name: "exclude takes priority over conflicting include",
			specs: ExtensionTestSpecs{
				{
					Name: "spec-aws-only",
					EnvironmentSelector: EnvironmentSelector{
						Include: PlatformEquals("aws"),
						Exclude: PlatformEquals("aws"),
					},
				},
			},
			envFlags: flags.EnvironmentalFlags{Platform: "aws"},
		},
		{
			name: "include based on facts",
			specs: ExtensionTestSpecs{
				{
					Name: "only-when-cool",
					EnvironmentSelector: EnvironmentSelector{
						Include: And(FactEquals("cool.component", "absolutely")),
					},
				},
				{
					Name: "only-when-super-cool",
					EnvironmentSelector: EnvironmentSelector{
						Include: And(FactEquals("super.cool.component", "absolutely")),
					},
				},
			},
			envFlags: flags.EnvironmentalFlags{Facts: map[string]string{"cool.component": "absolutely"}},
			want: ExtensionTestSpecs{
				{
					Name: "only-when-cool",
					EnvironmentSelector: EnvironmentSelector{
						Include: And(FactEquals("cool.component", "absolutely")),
					},
				},
			},
		},
		{
			name: "include based on optional capabilities",
			specs: ExtensionTestSpecs{
				{
					Name: "spec-baremetal-build",
					EnvironmentSelector: EnvironmentSelector{
						Include: OptionalCapabilitiesIncludeAny("baremetal", "build"),
					},
				},
				{
					Name: "spec-baremetal-only",
					EnvironmentSelector: EnvironmentSelector{
						Include: OptionalCapabilitiesIncludeAll("baremetal"),
					},
				},
				{
					Name: "spec-build-only",
					EnvironmentSelector: EnvironmentSelector{
						Include: OptionalCapabilitiesIncludeAll("build"),
					},
				},
				{
					Name: "spec-no-optional-capabilities-excluded",
					EnvironmentSelector: EnvironmentSelector{
						Exclude: NoOptionalCapabilitiesExist(),
					},
				},
			},
			envFlags: flags.EnvironmentalFlags{OptionalCapabilities: []string{"baremetal"}},
			want: ExtensionTestSpecs{
				{
					Name: "spec-baremetal-build",
					EnvironmentSelector: EnvironmentSelector{
						Include: OptionalCapabilitiesIncludeAny("baremetal", "build"),
					},
				},
				{
					Name: "spec-baremetal-only",
					EnvironmentSelector: EnvironmentSelector{
						Include: OptionalCapabilitiesIncludeAll("baremetal"),
					},
				},
				{
					Name: "spec-no-optional-capabilities-excluded",
					EnvironmentSelector: EnvironmentSelector{
						Exclude: NoOptionalCapabilitiesExist(),
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.specs.FilterByEnvironment(tc.envFlags)
			if diff := cmp.Diff(tc.wantErr, err, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("FilterByEnvironment returned unexpected error (-want +got): %s", diff)
			}
			if diff := cmp.Diff(tc.want, result, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("FilterByEnvironment returned unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSelect(t *testing.T) {
	testCases := []struct {
		name     string
		specs    ExtensionTestSpecs
		selectFn SelectFunction
		want     ExtensionTestSpecs
	}{
		{
			name: "name contains",
			specs: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "gcp-only",
				},
			},
			selectFn: NameContains("aws"),
			want: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
			},
		},
		{
			name: "name contains all",
			specs: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "aws-only-with-some-extra",
				},
			},
			selectFn: NameContainsAll("aws", "some-extra"),
			want: ExtensionTestSpecs{
				{
					Name: "aws-only-with-some-extra",
				},
			},
		},
		{
			name: "can return multiple",
			specs: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "gcp-only",
				},
				{
					Name: "another-aws-test",
				},
			},
			selectFn: NameContains("aws"),
			want: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "another-aws-test",
				},
			},
		},
		{
			name: "has label",
			specs: ExtensionTestSpecs{
				{
					Name:   "aws",
					Labels: sets.New("aws-test"),
				},
				{
					Name:   "gcp",
					Labels: sets.New("gcp-test"),
				},
			},
			selectFn: HasLabel("aws-test"),
			want: ExtensionTestSpecs{
				{
					Name:   "aws",
					Labels: sets.New("aws-test"),
				},
			},
		},
		{
			name: "has tag with value",
			specs: ExtensionTestSpecs{
				{
					Name: "aws",
					Tags: map[string]string{
						"tag-a": "val",
					},
				},
				{
					Name: "gcp",
					Tags: map[string]string{
						"tag-a": "another-val",
					},
				},
			},
			selectFn: HasTagWithValue("tag-a", "another-val"),
			want: ExtensionTestSpecs{
				{
					Name: "gcp",
					Tags: map[string]string{
						"tag-a": "another-val",
					},
				},
			},
		},
		{
			name: "with lifecycle",
			specs: ExtensionTestSpecs{
				{
					Name:      "aws",
					Lifecycle: LifecycleBlocking,
				},
				{
					Name:      "gcp",
					Lifecycle: LifecycleInforming,
				},
			},
			selectFn: WithLifecycle(LifecycleBlocking),
			want: ExtensionTestSpecs{
				{
					Name:      "aws",
					Lifecycle: LifecycleBlocking,
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.specs.Select(tc.selectFn)
			if diff := cmp.Diff(tc.want, result, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("Select returned unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMustSelect(t *testing.T) {
	testCases := []struct {
		name     string
		specs    ExtensionTestSpecs
		selectFn SelectFunction
		want     ExtensionTestSpecs
		wantErr  error
	}{
		{
			name: "expected to find specs",
			specs: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "gcp-only",
				},
			},
			selectFn: NameContains("aws"),
			want: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
			},
		},
		{
			name: "expected to not find specs",
			specs: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "gcp-only",
				},
			},
			selectFn: NameContains("azure"),
			want:     ExtensionTestSpecs{},
			wantErr:  errors.New("no specs selected with specified SelectFunctions"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.specs.MustSelect(tc.selectFn)
			if diff := cmp.Diff(err, tc.wantErr, cmp.AllowUnexported(ExtensionTestSpec{}), equateErrorMessage); diff != "" {
				t.Errorf("MustSelect returned unexpected error (-want +got): %s", diff)
			}
			if diff := cmp.Diff(tc.want, result, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("MustSelect returned unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSelectAny(t *testing.T) {
	testCases := []struct {
		name      string
		specs     ExtensionTestSpecs
		selectFns []SelectFunction
		want      ExtensionTestSpecs
	}{
		{
			name: "name contains",
			specs: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "azure-only",
				},
				{
					Name: "gcp-only",
				},
			},
			selectFns: []SelectFunction{NameContains("aws"), NameContains("gcp")},
			want: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "gcp-only",
				},
			},
		},
		{
			name: "has label or tag with value",
			specs: ExtensionTestSpecs{
				{
					Name:   "aws",
					Labels: sets.New("aws-test"),
					Tags: map[string]string{
						"tag-a": "val",
					},
				},
				{
					Name:   "excluded",
					Labels: sets.New("gcp-test"),
					Tags: map[string]string{
						"tag-a": "val",
					},
				},
				{
					Name:   "gcp",
					Labels: sets.New("gcp-test"),
					Tags: map[string]string{
						"tag-a": "another-val",
					},
				},
			},
			selectFns: []SelectFunction{HasLabel("aws-test"), HasTagWithValue("tag-a", "another-val")},
			want: ExtensionTestSpecs{
				{
					Name:   "aws",
					Labels: sets.New("aws-test"),
					Tags: map[string]string{
						"tag-a": "val",
					},
				},
				{
					Name:   "gcp",
					Labels: sets.New("gcp-test"),
					Tags: map[string]string{
						"tag-a": "another-val",
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.specs.SelectAny(tc.selectFns)
			if diff := cmp.Diff(tc.want, result, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("SelectAny returned unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMustSelectAny(t *testing.T) {
	testCases := []struct {
		name      string
		specs     ExtensionTestSpecs
		selectFns []SelectFunction
		want      ExtensionTestSpecs
		wantErr   error
	}{
		{
			name: "expected to find specs",
			specs: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "azure-only",
				},
				{
					Name: "gcp-only",
				},
			},
			selectFns: []SelectFunction{NameContains("aws"), NameContains("gcp")},
			want: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "gcp-only",
				},
			},
		},
		{
			name: "not expected to find specs",
			specs: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "azure-only",
				},
				{
					Name: "gcp-only",
				},
			},
			selectFns: []SelectFunction{NameContains("baremetal"), NameContains("vsphere")},
			want:      ExtensionTestSpecs{},
			wantErr:   errors.New("no specs selected with specified SelectFunctions"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.specs.MustSelectAny(tc.selectFns)
			if diff := cmp.Diff(err, tc.wantErr, cmp.AllowUnexported(ExtensionTestSpec{}), equateErrorMessage); diff != "" {
				t.Errorf("MustSelect returned unexpected error (-want +got): %s", diff)
			}
			if diff := cmp.Diff(tc.want, result, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("SelectAny returned unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSelectAll(t *testing.T) {
	testCases := []struct {
		name      string
		specs     ExtensionTestSpecs
		selectFns []SelectFunction
		want      ExtensionTestSpecs
	}{
		{
			name: "name contains",
			specs: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "azure-only",
				},
				{
					Name: "aws-test",
				},
			},
			selectFns: []SelectFunction{NameContains("aws"), NameContains("test")},
			want: ExtensionTestSpecs{
				{
					Name: "aws-test",
				},
			},
		},
		{
			name: "has label and tag with value",
			specs: ExtensionTestSpecs{
				{
					Name:   "aws",
					Labels: sets.New("aws-test"),
					Tags: map[string]string{
						"tag-a": "good-val",
					},
				},
				{
					Name:   "excluded",
					Labels: sets.New("aws-test"),
					Tags: map[string]string{
						"tag-a": "val",
					},
				},
				{
					Name:   "gcp",
					Labels: sets.New("gcp-test"),
					Tags: map[string]string{
						"tag-a": "good-val",
					},
				},
			},
			selectFns: []SelectFunction{HasLabel("aws-test"), HasTagWithValue("tag-a", "good-val")},
			want: ExtensionTestSpecs{
				{
					Name:   "aws",
					Labels: sets.New("aws-test"),
					Tags: map[string]string{
						"tag-a": "good-val",
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.specs.SelectAll(tc.selectFns)
			if diff := cmp.Diff(tc.want, result, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("SelectAny returned unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMustSelectAll(t *testing.T) {
	testCases := []struct {
		name      string
		specs     ExtensionTestSpecs
		selectFns []SelectFunction
		want      ExtensionTestSpecs
		wantErr   error
	}{
		{
			name: "expected to find specs",
			specs: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "azure-only",
				},
				{
					Name: "aws-test",
				},
			},
			selectFns: []SelectFunction{NameContains("aws"), NameContains("test")},
			want: ExtensionTestSpecs{
				{
					Name: "aws-test",
				},
			},
		},
		{
			name: "not expected to find specs",
			specs: ExtensionTestSpecs{
				{
					Name: "aws-only",
				},
				{
					Name: "azure-only",
				},
				{
					Name: "aws-test",
				},
			},
			selectFns: []SelectFunction{NameContains("baremetal"), NameContains("test")},
			want:      ExtensionTestSpecs{},
			wantErr:   errors.New("no specs selected with specified SelectFunctions"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.specs.MustSelectAll(tc.selectFns)
			if diff := cmp.Diff(err, tc.wantErr, cmp.AllowUnexported(ExtensionTestSpec{}), equateErrorMessage); diff != "" {
				t.Errorf("MustSelect returned unexpected error (-want +got): %s", diff)
			}
			if diff := cmp.Diff(tc.want, result, cmp.AllowUnexported(ExtensionTestSpec{})); diff != "" {
				t.Errorf("SelectAny returned unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExtensionTestSpecs_Run_LifecycleFailures(t *testing.T) {
	testCases := []struct {
		name        string
		specs       ExtensionTestSpecs
		wantErr     bool
		errContains string
	}{
		{
			name: "only informing tests fail - no error",
			specs: ExtensionTestSpecs{
				{
					Name:      "informing-test-1",
					Lifecycle: LifecycleInforming,
					Run: func(ctx context.Context) *ExtensionTestResult {
						return &ExtensionTestResult{
							Name:   "informing-test-1",
							Result: ResultFailed,
						}
					},
				},
				{
					Name:      "informing-test-2",
					Lifecycle: LifecycleInforming,
					Run: func(ctx context.Context) *ExtensionTestResult {
						return &ExtensionTestResult{
							Name:   "informing-test-2",
							Result: ResultFailed,
						}
					},
				},
			},
			wantErr: false,
		},
		{
			name: "blocking test fails - returns error",
			specs: ExtensionTestSpecs{
				{
					Name:      "blocking-test",
					Lifecycle: LifecycleBlocking,
					Run: func(ctx context.Context) *ExtensionTestResult {
						return &ExtensionTestResult{
							Name:   "blocking-test",
							Result: ResultFailed,
						}
					},
				},
			},
			wantErr:     true,
			errContains: "1 tests failed",
		},
		{
			name: "both blocking and informing fail - returns error with counts",
			specs: ExtensionTestSpecs{
				{
					Name:      "blocking-test",
					Lifecycle: LifecycleBlocking,
					Run: func(ctx context.Context) *ExtensionTestResult {
						return &ExtensionTestResult{
							Name:   "blocking-test",
							Result: ResultFailed,
						}
					},
				},
				{
					Name:      "informing-test",
					Lifecycle: LifecycleInforming,
					Run: func(ctx context.Context) *ExtensionTestResult {
						return &ExtensionTestResult{
							Name:   "informing-test",
							Result: ResultFailed,
						}
					},
				},
			},
			wantErr:     true,
			errContains: "2 tests failed (1 informing)",
		},
		{
			name: "all tests pass - no error",
			specs: ExtensionTestSpecs{
				{
					Name:      "blocking-test",
					Lifecycle: LifecycleBlocking,
					Run: func(ctx context.Context) *ExtensionTestResult {
						return &ExtensionTestResult{
							Name:   "blocking-test",
							Result: ResultPassed,
						}
					},
				},
				{
					Name:      "informing-test",
					Lifecycle: LifecycleInforming,
					Run: func(ctx context.Context) *ExtensionTestResult {
						return &ExtensionTestResult{
							Name:   "informing-test",
							Result: ResultPassed,
						}
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.specs.Run(context.TODO(), NullResultWriter{}, 1)
			if tc.wantErr {
				if err == nil {
					t.Errorf("Expected error but got nil")
				} else if tc.errContains != "" && !assert.Contains(t, err.Error(), tc.errContains) {
					t.Errorf("Expected error to contain %q, got %q", tc.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
			}
		})
	}
}

func TestModuleTestsOnly(t *testing.T) {
	testCases := []struct {
		name string
		spec *ExtensionTestSpec
		want bool
	}{
		{
			name: "excluded - all k8s.io/kubernetes/test code locations",
			spec: &ExtensionTestSpec{
				Name: "[sig-cli] Kubectl rollout undo undo should rollback and update deployment env",
				CodeLocations: []string{
					"k8s.io/kubernetes@v1.33.3/test/e2e/kubectl/rollout.go:40",
					"set up framework | framework.go:200",
					"k8s.io/kubernetes@v1.33.3/test/e2e/framework/node/init/init.go:33",
					"k8s.io/kubernetes@v1.33.3/test/e2e/framework/debug/init/init.go:60",
					"k8s.io/kubernetes@v1.33.3/test/e2e/framework/metrics/init/init.go:33",
					"k8s.io/kubernetes@v1.33.3/test/e2e/kubectl/rollout.go:48",
					"k8s.io/kubernetes@v1.33.3/test/e2e/kubectl/rollout.go:54",
					"k8s.io/kubernetes@v1.33.3/test/e2e/kubectl/rollout.go:55",
					"k8s.io/kubernetes@v1.33.3/test/e2e/kubectl/rollout.go:58",
				},
			},
			want: false,
		},
		{
			name: "included - has local code locations, in module format",
			spec: &ExtensionTestSpec{
				Name: "[sig-cluster-lifecycle][OCPFeatureGate:VSphereHostVMGroupZonal][platform:vsphere] A Machine in a managed cluster should be placed in the correct vm-host group",
				CodeLocations: []string{
					"github.com/openshift/machine-api-operator@v0.0.0/test/e2e/vsphere/hostzonal.go:32",
					"github.com/openshift/machine-api-operator@v0.0.0/test/e2e/vsphere/hostzonal.go:46",
					"github.com/openshift/machine-api-operator@v0.0.0/test/e2e/vsphere/hostzonal.go:72",
				},
			},
			want: true,
		},
		{
			name: "included - has local relative path code locations",
			spec: &ExtensionTestSpec{
				Name: "[sig-cluster-lifecycle][OCPFeatureGate:VSphereMultiNetworks][platform:vsphere] Managed cluster should new machines should pass multi network tests",
				CodeLocations: []string{
					"test/e2e/vsphere/multi-nic.go:152",
					"test/e2e/vsphere/multi-nic.go:171",
					"test/e2e/vsphere/multi-nic.go:238",
				},
			},
			want: true,
		},
	}

	selectFn := ModuleTestsOnly()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := selectFn(tc.spec)
			if result != tc.want {
				t.Errorf("ModuleTestsOnly() returned %v, want %v", result, tc.want)
			}
		})
	}
}

// TestExtensionTestSpecs_Run_UnknownResultNormalized verifies that a blocking in-process
// test returning an unknown result type (e.g. empty string) is normalized to ResultFailed
// before failure counting, so run-suite exits non-zero and JUnit is consistent.
func TestExtensionTestSpecs_Run_UnknownResultNormalized(t *testing.T) {
	specs := ExtensionTestSpecs{
		{
			Name:      "blocking-test-with-unknown-result",
			Lifecycle: LifecycleBlocking,
			Run: func(ctx context.Context) *ExtensionTestResult {
				return &ExtensionTestResult{
					Name:   "blocking-test-with-unknown-result",
					Result: Result(""),
				}
			},
		},
	}

	results, err := specs.Run(context.TODO(), NullResultWriter{}, 1)

	if err == nil {
		t.Error("expected non-nil error (non-zero exit) for blocking test with unknown result, got nil")
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Result != ResultFailed {
		t.Errorf("result.Result = %q, want %q", results[0].Result, ResultFailed)
	}
	if results[0].Error == "" {
		t.Error("result.Error is empty; expected diagnostic about unknown result type")
	}
}

// equateErrorMessage reports errors to be equal if both are nil
// or both have the same message.
var equateErrorMessage = cmp.FilterValues(func(x, y interface{}) bool {
	_, ok1 := x.(error)
	_, ok2 := y.(error)
	return ok1 && ok2
}, cmp.Comparer(func(x, y interface{}) bool {
	xe := x.(error)
	ye := y.(error)
	if xe == nil || ye == nil {
		return xe == nil && ye == nil
	}
	return xe.Error() == ye.Error()
}))
