package ginkgo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
)

const spawnEnvMarker = "OTE_SPAWN_ENV_MARKER"

type spawnChildProbe struct {
	Marker       string `json:"marker"`
	SpawnedChild string `json:"spawnedChild"`
	Timeout      string `json:"timeout"`
}

// TestMain intercepts `run-test` so SpawnProcessToRunTest* can exec this package's
// test binary as a child without needing a full OTE cobra command.
func TestMain(m *testing.M) {
	for _, arg := range os.Args[1:] {
		if arg == "run-test" {
			if err := writeSpawnChildResult(); err != nil {
				fmt.Fprintf(os.Stderr, "write spawn child result: %v\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func writeSpawnChildResult() error {
	probe, err := json.Marshal(spawnChildProbe{
		Marker:       os.Getenv(spawnEnvMarker),
		SpawnedChild: os.Getenv(extensiontests.SpawnedChildEnv),
		Timeout:      spawnChildTimeout(),
	})
	if err != nil {
		return err
	}
	res := extensiontests.ExtensionTestResult{
		Name:   os.Args[len(os.Args)-1],
		Result: extensiontests.ResultPassed,
		Output: string(probe),
	}
	return json.NewEncoder(os.Stdout).Encode(res)
}

func spawnChildTimeout() string {
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--timeout=") {
			return strings.TrimPrefix(arg, "--timeout=")
		}
	}
	return ""
}

func spawnCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func childProbe(t *testing.T, res *extensiontests.ExtensionTestResult) spawnChildProbe {
	t.Helper()
	if res == nil {
		t.Fatal("expected a result from spawned child")
	}
	if res.Result != extensiontests.ResultPassed {
		t.Fatalf("child result=%s error=%q output=%q", res.Result, res.Error, res.Output)
	}
	var probe spawnChildProbe
	if err := json.Unmarshal([]byte(res.Output), &probe); err != nil {
		t.Fatalf("parse child probe from output %q: %v", res.Output, err)
	}
	return probe
}

func TestSpawnProcessToRunTestWithEnvReachesChild(t *testing.T) {
	t.Setenv(spawnEnvMarker, "parent")
	ctx := spawnCtx(t)
	timeout := 5 * time.Second

	t.Run("nil env inherits parent and sets spawned-child marker", func(t *testing.T) {
		probe := childProbe(t, SpawnProcessToRunTest(ctx, "env-probe", timeout))
		if probe.Marker != "parent" {
			t.Errorf("child env marker: got %q, want parent", probe.Marker)
		}
		if probe.SpawnedChild != "1" {
			t.Errorf("spawned-child marker: got %q, want 1", probe.SpawnedChild)
		}
	})

	t.Run("empty env inherits parent", func(t *testing.T) {
		probe := childProbe(t, SpawnProcessToRunTestWithEnv(ctx, "env-probe", timeout, map[string]string{}))
		if probe.Marker != "parent" {
			t.Errorf("child env marker: got %q, want parent", probe.Marker)
		}
		if probe.SpawnedChild != "1" {
			t.Errorf("spawned-child marker: got %q, want 1", probe.SpawnedChild)
		}
	})

	t.Run("extra env overrides parent", func(t *testing.T) {
		probe := childProbe(t, SpawnProcessToRunTestWithEnv(ctx, "env-probe", timeout, map[string]string{
			spawnEnvMarker: "child",
		}))
		if probe.Marker != "child" {
			t.Errorf("child env marker: got %q, want child", probe.Marker)
		}
		if probe.SpawnedChild != "1" {
			t.Errorf("spawned-child marker: got %q, want 1", probe.SpawnedChild)
		}
	})
}

func TestSpawnProcessToRunTestWithEnvRejectsInvalidEnvironment(t *testing.T) {
	res := SpawnProcessToRunTestWithEnv(spawnCtx(t), "env-probe", 5*time.Second, map[string]string{
		"OTE_SPAWNED_CHILD=shadow": "1",
	})
	if res.Result != extensiontests.ResultFailed {
		t.Fatalf("result: got %q, want %q", res.Result, extensiontests.ResultFailed)
	}
	if !strings.Contains(res.Error, "contains '=' or NUL") {
		t.Fatalf("error did not explain invalid environment key: %q", res.Error)
	}
}

func TestRunParallelWithSpecEnvReadsEnvAtCallTime(t *testing.T) {
	ctx := spawnCtx(t)
	spec := &extensiontests.ExtensionTestSpec{}
	run := runParallelWithSpecEnv(spec, "env-probe")

	// Env and Timeout are assigned after the closure is created, matching BeforeSpawn.
	spec.Env = map[string]string{spawnEnvMarker: "from-spec"}
	spec.Timeout = 7 * time.Second

	probe := childProbe(t, run(ctx))
	if probe.Marker != "from-spec" {
		t.Errorf("child env marker: got %q, want from-spec", probe.Marker)
	}
	if probe.Timeout != "7s" {
		t.Errorf("child timeout: got %q, want 7s", probe.Timeout)
	}
	if probe.SpawnedChild != "1" {
		t.Errorf("spawned-child marker: got %q, want 1", probe.SpawnedChild)
	}
}

func TestRunParallelWithSpecEnvDefaultTimeout(t *testing.T) {
	ctx := spawnCtx(t)
	spec := &extensiontests.ExtensionTestSpec{
		Env: map[string]string{spawnEnvMarker: "default-timeout"},
	}
	run := runParallelWithSpecEnv(spec, "env-probe")
	probe := childProbe(t, run(ctx))
	if probe.Timeout != (90 * time.Minute).String() {
		t.Errorf("default timeout: got %q, want %s", probe.Timeout, 90*time.Minute)
	}
}
