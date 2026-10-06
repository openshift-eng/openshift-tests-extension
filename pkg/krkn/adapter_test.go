package krkn

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	ext "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
)

func TestBuildExtensionTestSpecsFromKrknScenarios(t *testing.T) {
	scenarios := []ScenarioSpec{
		{
			Name:       "[sig-chaos] etcd pod disruption",
			ScriptPath: "scenarios/etcd_pod_disruption.py",
			Tags:       []string{"SLOW", "DISRUPTIVE"},
			EnvVars:    map[string]string{"NAMESPACE": "openshift-etcd"},
			IncludeCEL: `platform == "aws"`,
			Informing:  true,
		},
		{
			Name:       "[sig-chaos] kube-apiserver disruption",
			ScriptPath: "scenarios/kube_apiserver_disruption.py",
		},
	}

	metadata, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatalf("marshal scenarios: %v", err)
	}

	scriptFS := fstest.MapFS{
		"scenarios/etcd_pod_disruption.py":       &fstest.MapFile{Data: []byte("import sys\nsys.exit(0)\n")},
		"scenarios/kube_apiserver_disruption.py": &fstest.MapFile{Data: []byte("import sys\nsys.exit(0)\n")},
	}

	specs, err := BuildExtensionTestSpecsFromKrknScenarios(metadata, scriptFS)
	if err != nil {
		t.Fatalf("BuildExtensionTestSpecsFromKrknScenarios returned error: %v", err)
	}

	if len(specs) != len(scenarios) {
		t.Fatalf("expected %d specs, got %d", len(scenarios), len(specs))
	}

	for i, spec := range specs {
		sc := scenarios[i]

		if spec.Name != sc.Name {
			t.Errorf("spec[%d].Name = %q, want %q", i, spec.Name, sc.Name)
		}

		// Every scenario must share the same Conflict key so the scheduler
		// serializes chaos scenarios against each other.
		if len(spec.Resources.Isolation.Conflict) != 1 || spec.Resources.Isolation.Conflict[0] != chaosConflictKey {
			t.Errorf("spec[%d].Resources.Isolation.Conflict = %v, want [%q]", i, spec.Resources.Isolation.Conflict, chaosConflictKey)
		}

		// Every scenario must also share the same Taint key so unrelated,
		// non-chaos tests are fenced off while a chaos scenario is running.
		if len(spec.Resources.Isolation.Taint) != 1 || spec.Resources.Isolation.Taint[0] != chaosTaintKey {
			t.Errorf("spec[%d].Resources.Isolation.Taint = %v, want [%q]", i, spec.Resources.Isolation.Taint, chaosTaintKey)
		}

		// Mode is not consulted by the scheduler; it must be left unset so
		// nobody mistakes it for the real isolation mechanism.
		if spec.Resources.Isolation.Mode != "" {
			t.Errorf("spec[%d].Resources.Isolation.Mode = %q, want empty (unused by scheduler)", i, spec.Resources.Isolation.Mode)
		}

		if spec.Run == nil {
			t.Errorf("spec[%d].Run is nil, want non-nil", i)
		}
		if spec.RunParallel == nil {
			t.Errorf("spec[%d].RunParallel is nil, want non-nil", i)
		}

		wantLifecycle := ext.LifecycleBlocking
		if sc.Informing {
			wantLifecycle = ext.LifecycleInforming
		}
		if spec.Lifecycle != wantLifecycle {
			t.Errorf("spec[%d].Lifecycle = %q, want %q", i, spec.Lifecycle, wantLifecycle)
		}

		// spec.Timeout must be populated so the adapter has something to
		// enforce at runtime (see runKrknScenario) even when run-suite never
		// gets a chance to backfill it from Suite.TestTimeout.
		if spec.Timeout <= 0 {
			t.Errorf("spec[%d].Timeout = %v, want > 0", i, spec.Timeout)
		}
	}
}

func TestBuildExtensionTestSpecsFromKrknScenarios_PerScenarioTimeout(t *testing.T) {
	scenarios := []ScenarioSpec{
		{Name: "custom-timeout", ScriptPath: "a.py", Timeout: "45m"},
		{Name: "invalid-timeout", ScriptPath: "b.py", Timeout: "not-a-duration"},
		{Name: "default-timeout", ScriptPath: "c.py"},
	}
	metadata, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatalf("marshal scenarios: %v", err)
	}

	scriptFS := fstest.MapFS{
		"a.py": &fstest.MapFile{Data: []byte("")},
		"b.py": &fstest.MapFile{Data: []byte("")},
		"c.py": &fstest.MapFile{Data: []byte("")},
	}

	specs, err := BuildExtensionTestSpecsFromKrknScenarios(metadata, scriptFS)
	if err != nil {
		t.Fatalf("BuildExtensionTestSpecsFromKrknScenarios returned error: %v", err)
	}

	if got, want := specs[0].Timeout, 45*time.Minute; got != want {
		t.Errorf("custom-timeout spec.Timeout = %v, want %v", got, want)
	}
	if got, want := specs[0].Resources.Timeout, "45m"; got != want {
		t.Errorf("custom-timeout Resources.Timeout = %q, want %q", got, want)
	}
	if got, want := specs[1].Timeout, chaosDefaultTimeout; got != want {
		t.Errorf("invalid-timeout spec.Timeout = %v, want fallback %v", got, want)
	}
	if got, want := specs[1].Resources.Timeout, chaosDefaultTimeoutStr; got != want {
		t.Errorf("invalid-timeout Resources.Timeout = %q, want fallback %q", got, want)
	}
	if got, want := specs[2].Timeout, chaosDefaultTimeout; got != want {
		t.Errorf("default-timeout spec.Timeout = %v, want fallback %v", got, want)
	}
	if got, want := specs[2].Resources.Timeout, chaosDefaultTimeoutStr; got != want {
		t.Errorf("default-timeout Resources.Timeout = %q, want fallback %q", got, want)
	}
}

func TestRunKrknScenario_PassAndFail(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available in test environment")
	}

	scriptFS := fstest.MapFS{
		"pass.py": &fstest.MapFile{Data: []byte("import sys\nsys.exit(0)\n")},
		"fail.py": &fstest.MapFile{Data: []byte("import sys\nsys.exit(1)\n")},
	}

	passResult := runKrknScenario(context.Background(), ScenarioSpec{Name: "pass", ScriptPath: "pass.py"}, scriptFS, time.Minute)
	if passResult.Result != ext.ResultPassed {
		t.Errorf("pass scenario result = %q, want %q (output: %s)", passResult.Result, ext.ResultPassed, passResult.Output)
	}

	failResult := runKrknScenario(context.Background(), ScenarioSpec{Name: "fail", ScriptPath: "fail.py"}, scriptFS, time.Minute)
	if failResult.Result != ext.ResultFailed {
		t.Errorf("fail scenario result = %q, want %q (output: %s)", failResult.Result, ext.ResultFailed, failResult.Output)
	}
}

func TestRunKrknScenario_TimeoutKillsProcessGroup(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available in test environment")
	}

	// Sleeps far longer than the enforced timeout; if the timeout isn't
	// actually enforced this test will hang/time out at the `go test` level.
	scriptFS := fstest.MapFS{
		"hang.py": &fstest.MapFile{Data: []byte("import time\ntime.sleep(300)\n")},
	}

	start := time.Now()
	result := runKrknScenario(context.Background(), ScenarioSpec{Name: "hang", ScriptPath: "hang.py"}, scriptFS, 500*time.Millisecond)
	elapsed := time.Since(start)

	if result.Result != ext.ResultFailed {
		t.Errorf("result = %q, want %q", result.Result, ext.ResultFailed)
	}
	if !strings.Contains(result.Error, "timed out") {
		t.Errorf("result.Error = %q, want it to mention timeout", result.Error)
	}
	// Should be killed shortly after the 500ms timeout, not run anywhere
	// close to the full 300s sleep.
	if elapsed > 10*time.Second {
		t.Errorf("scenario took %v to be killed after timeout, want well under 10s", elapsed)
	}
}

func TestBuildExtensionTestSpecsFromKrknScenarios_RejectsIncompleteSpec(t *testing.T) {
	metadata, err := json.Marshal([]ScenarioSpec{{Name: "no-command"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = BuildExtensionTestSpecsFromKrknScenarios(metadata, nil)
	if err == nil {
		t.Fatal("expected error for spec with neither entrypoint nor scriptPath")
	}
}

func TestBuildExtensionTestSpecsFromKrknScenarios_EntrypointOnly(t *testing.T) {
	// A node-style spec (hub script + env) must take the same Build path as a
	// pod-style python prototype spec — no adapter branch on name/tags.
	scenarios := []ScenarioSpec{
		{
			Name:       "[sig-chaos] cluster power outage",
			Entrypoint: "/bin/bash",
			Args:       []string{"/home/krkn/krkn-hub/ote_wrapper.sh", "./power-outage/prow_run.sh"},
			WorkDir:    "/home/krkn/krkn-hub",
			Tags:       []string{"SLOW", "DISRUPTIVE", "node"},
			EnvVars:    map[string]string{"CLOUD_TYPE": "aws", "WAIT_DURATION": "600"},
			EnvFrom:    map[string]string{"TIMEOUT": "POWER_OUTAGE_TIMEOUT"},
			IncludeCEL: `platform == "aws"`,
			Timeout:    "45m",
			Informing:  true,
		},
	}
	metadata, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	specs, err := BuildExtensionTestSpecsFromKrknScenarios(metadata, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("got %d specs, want 1", len(specs))
	}
	if specs[0].Timeout != 45*time.Minute {
		t.Errorf("Timeout = %v, want 45m", specs[0].Timeout)
	}
	if specs[0].Resources.Timeout != "45m" {
		t.Errorf("Resources.Timeout = %q, want 45m", specs[0].Resources.Timeout)
	}
	if !specs[0].Labels.Has("node") {
		t.Errorf("Labels missing node; Has(node)=false")
	}
}

func TestBuildEnv_EnvFromAndOverridePrecedence(t *testing.T) {
	t.Setenv("POWER_OUTAGE_TIMEOUT", "1200")
	t.Setenv("TIMEOUT", "600")
	t.Setenv("KUBECONFIG", "/tmp/kube")

	env := buildEnv(
		map[string]string{"CLOUD_TYPE": "aws", "TIMEOUT": "999"},
		map[string]string{"TIMEOUT": "POWER_OUTAGE_TIMEOUT"},
	)
	got := envMap(env)

	if got["CLOUD_TYPE"] != "aws" {
		t.Errorf("CLOUD_TYPE = %q, want aws", got["CLOUD_TYPE"])
	}
	// EnvVars win over EnvFrom, which copies ambient POWER_OUTAGE_TIMEOUT.
	if got["TIMEOUT"] != "999" {
		t.Errorf("TIMEOUT = %q, want 999 (EnvVars beat EnvFrom)", got["TIMEOUT"])
	}
	if got["KRKN_KUBE_CONFIG"] != "/tmp/kube" {
		t.Errorf("KRKN_KUBE_CONFIG = %q, want /tmp/kube", got["KRKN_KUBE_CONFIG"])
	}

	env = buildEnv(nil, map[string]string{"TIMEOUT": "POWER_OUTAGE_TIMEOUT"})
	got = envMap(env)
	if got["TIMEOUT"] != "1200" {
		t.Errorf("TIMEOUT after EnvFrom only = %q, want 1200", got["TIMEOUT"])
	}
}

func TestRunKrknScenario_Entrypoint(t *testing.T) {
	result := runKrknScenario(context.Background(), ScenarioSpec{
		Name:       "true",
		Entrypoint: "/bin/true",
	}, nil, time.Minute)
	if result.Result != ext.ResultPassed {
		t.Errorf("result = %q (%s), want passed", result.Result, result.Error)
	}
}

func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, ok := splitEnv(kv)
		if ok {
			m[k] = v
		}
	}
	return m
}

func TestBoundedBuffer_TruncatesToTail(t *testing.T) {
	b := newBoundedBuffer(10)
	_, _ = b.Write([]byte("0123456789"))
	_, _ = b.Write([]byte("ABCDE"))

	got := b.String()
	if !strings.Contains(got, "56789ABCDE") {
		t.Errorf("String() = %q, want it to contain the last 10 bytes %q", got, "56789ABCDE")
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("String() = %q, want a truncation notice", got)
	}
}
