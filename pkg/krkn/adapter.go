package krkn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	ext "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"
)

const (
	// chaosConflictKey is the shared Isolation.Conflict value applied to every
	// krkn scenario spec. The scheduler serializes any specs that share a
	// Conflict entry, so this guarantees chaos scenarios never run
	// concurrently with each other regardless of worker count.
	//
	// NOTE: Isolation.Mode is not consulted by the scheduler (see
	// pkg/extension/extensiontests/scheduler.go) and must not be relied upon.
	// Conflict is the mechanism that actually enforces mutual exclusion
	// between chaos specs themselves.
	chaosConflictKey = "chaos-disruption"

	// chaosTaintKey is applied to every krkn scenario spec's Isolation.Taint.
	// Verified directly against pkg/extension/extensiontests/scheduler.go
	// (canTolerateTaints / activeTaints / MarkTestComplete, plus scheduler
	// unit tests) that Taint/Toleration is fully implemented and enforced:
	// any spec without a matching Toleration is held back by the scheduler
	// while a taint is active. Without this, chaos scenarios (etcd kill,
	// kube-apiserver disruption, etc.) could run concurrently with unrelated
	// non-chaos tests in the same job/suite, producing deterministic-but-
	// looks-flaky failures elsewhere. Conflict alone only serialises chaos
	// specs against each other; Taint is what fences off everything else.
	chaosTaintKey = "chaos-disruption"

	// chaosDefaultTimeoutStr is the Resources.Timeout metadata value used when
	// ScenarioSpec.Timeout is empty or does not parse as a duration > 0.
	// Resources.Timeout is informational only. The value actually enforced by
	// this adapter is chaosDefaultTimeout / ScenarioSpec.Timeout, applied
	// explicitly in runKrknScenario below -- ExtensionTestSpec.Timeout is
	// NOT read by ExtensionTestSpecs.Run()/runSpec() for custom Run/RunParallel
	// implementations like this one (it's only consulted by the ginkgo
	// subprocess-spawning helper in pkg/ginkgo). Relying on it here would
	// silently leave scenarios with no enforced timeout at all when run via
	// `run-suite` (which never puts a deadline on the context it passes to
	// Run/RunParallel).
	chaosDefaultTimeoutStr = "30m"
	chaosDefaultTimeout    = 30 * time.Minute

	// chaosShutdownGracePeriod is how long we wait after SIGTERM before
	// escalating to SIGKILL when a scenario is cancelled or times out. Chaos
	// scenarios often have cleanup/rollback logic (undo a pod kill, restore a
	// scaled-down deployment, etc.) that needs a chance to run; an instant
	// SIGKILL would skip that and could leave the cluster in a broken state
	// for the next test.
	chaosShutdownGracePeriod = 30 * time.Second

	// maxCapturedOutputBytes bounds how much combined stdout+stderr is kept
	// in memory / in the result. Chaos scripts can be extremely chatty
	// (debug logging, kubectl/oc command echoes); capturing unbounded output
	// risks OOM-killing the test runner process itself. We keep the tail,
	// since that's most likely to contain the actual failure.
	maxCapturedOutputBytes = 512 * 1024
)

// BuildExtensionTestSpecsFromKrknScenarios parses a JSON array of ScenarioSpec
// entries and produces one ExtensionTestSpec per entry. The same code path is
// used for every scenario; differences come only from ScenarioSpec fields.
//
// This function is a prototype. The local production copy is
// /home/oharan/chaos-tests-extension (package adapter). GitHub
// redhat-chaos/chaos-tests-extension remains unpublished (404).
//
// Each spec must set Name and at least one of Entrypoint (production: exec
// /bin/bash with args ote_wrapper.sh then krkn-hub prow_run.sh already on the
// runner image) or ScriptPath (prototype: extract from scriptFS and run with
// python3).
//
// scriptFS is only consulted when ScriptPath is set. Production hub specs
// leave it unused (pass a nil or empty fs.FS).
//
// Isolation: every scenario shares a single Conflict key so the scheduler
// serializes them against one another, AND a shared Taint key so that no
// unrelated (non-chaos) test is scheduled concurrently with a running chaos
// scenario. See chaosConflictKey / chaosTaintKey for details.
//
// Usage in a binary:
//
//	specs, err := krkn.BuildExtensionTestSpecsFromKrknScenarios(scenarioMetadata, nil)
func BuildExtensionTestSpecsFromKrknScenarios(metadata []byte, scriptFS fs.FS) (ext.ExtensionTestSpecs, error) {
	var scenarios []ScenarioSpec
	if err := json.Unmarshal(metadata, &scenarios); err != nil {
		return nil, fmt.Errorf("failed to unmarshal krkn scenario metadata: %w", err)
	}

	specs := make(ext.ExtensionTestSpecs, 0, len(scenarios))
	for _, sc := range scenarios {
		sc := sc // capture loop variable

		if err := validateScenarioSpec(sc); err != nil {
			return nil, err
		}

		lifecycle := ext.LifecycleBlocking
		if sc.Informing {
			lifecycle = ext.LifecycleInforming
		}

		spec := &ext.ExtensionTestSpec{
			Name:          sc.Name,
			Labels:        sets.New(sc.Tags...),
			CodeLocations: []string{codeLocation(sc)},
			Lifecycle:     lifecycle,
			// Chaos scenarios mutate cluster state; serialise them against
			// each other via a shared Conflict key so the scheduler never
			// dispatches two of them at the same time, AND taint the run so
			// unrelated non-chaos tests are held back while a chaos scenario
			// is in flight. See chaosConflictKey / chaosTaintKey.
			Resources: ext.Resources{
				Isolation: ext.Isolation{
					Conflict: []string{chaosConflictKey},
					Taint:    []string{chaosTaintKey},
				},
				Timeout: resolveTimeoutStr(sc.Timeout),
			},
			EnvironmentSelector: ext.EnvironmentSelector{
				Include: sc.IncludeCEL,
				Exclude: sc.ExcludeCEL,
			},
			// Populated so that Suite.TestTimeout / manual overrides have a
			// well-defined starting point (runsuite.go only overrides this
			// when it is still zero). The value actually enforced at runtime
			// is read back out of this field in runKrknScenario.
			Timeout: resolveTimeout(sc.Timeout, chaosDefaultTimeout),
		}

		runFn := func(ctx context.Context) *ext.ExtensionTestResult {
			// Read spec.Timeout (not sc.Timeout) at call time: it may have
			// been overridden after Build returned (e.g. by a Suite's
			// TestTimeout in runsuite.go).
			timeout := spec.Timeout
			if timeout <= 0 {
				timeout = chaosDefaultTimeout
			}
			return runKrknScenario(ctx, sc, scriptFS, timeout)
		}
		spec.Run = runFn
		spec.RunParallel = runFn

		specs = append(specs, spec)
	}

	return specs, nil
}

func validateScenarioSpec(sc ScenarioSpec) error {
	if sc.Name == "" {
		return fmt.Errorf("scenario spec is missing name")
	}
	if sc.Entrypoint == "" && sc.ScriptPath == "" {
		return fmt.Errorf("scenario %q must set entrypoint or scriptPath", sc.Name)
	}
	return nil
}

func codeLocation(sc ScenarioSpec) string {
	if sc.Entrypoint != "" {
		if sc.WorkDir != "" {
			return sc.WorkDir + " " + sc.Entrypoint
		}
		return sc.Entrypoint
	}
	return sc.ScriptPath
}

// resolveTimeout parses timeoutStr (e.g. "10m"); if it is empty or invalid,
// fallback is returned instead.
func resolveTimeout(timeoutStr string, fallback time.Duration) time.Duration {
	if timeoutStr == "" {
		return fallback
	}
	d, err := time.ParseDuration(timeoutStr)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// resolveTimeoutStr returns timeoutStr for Resources.Timeout metadata when it
// parses as a duration > 0; otherwise chaosDefaultTimeoutStr ("30m").
func resolveTimeoutStr(timeoutStr string) string {
	if timeoutStr == "" {
		return chaosDefaultTimeoutStr
	}
	d, err := time.ParseDuration(timeoutStr)
	if err != nil || d <= 0 {
		return chaosDefaultTimeoutStr
	}
	return timeoutStr
}

// runKrknScenario execs the spec's command. Production specs set Entrypoint
// (krkn-hub script already on the runner image). Prototype specs set
// ScriptPath, which is extracted from scriptFS and run with python3.
//
// The adapter does not interpret the scenario name or plugin type. Env,
// timeout, workdir, and argv all come from ScenarioSpec.
//
// The process runs in its own process group so that on cancellation/timeout
// we can signal the whole group (covering any kubectl/oc/child processes the
// scenario itself spawns), not just the immediate child. Shutdown is graceful
// (SIGTERM, then SIGKILL after chaosShutdownGracePeriod) so cleanup/rollback
// code gets a chance to run.
func runKrknScenario(ctx context.Context, sc ScenarioSpec, scriptFS fs.FS, timeout time.Duration) *ext.ExtensionTestResult {
	result := &ext.ExtensionTestResult{
		Name: sc.Name,
	}

	argv, cleanup, err := resolveCommand(sc, scriptFS)
	if err != nil {
		result.Result = ext.ResultFailed
		result.Error = err.Error()
		return result
	}
	defer cleanup()

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out := newBoundedBuffer(maxCapturedOutputBytes)

	// Deliberately exec.Command (not exec.CommandContext): CommandContext
	// would SIGKILL the direct child the instant runCtx is done, giving the
	// scenario's cleanup code no chance to run and leaving any grandchild
	// processes (kubectl/oc) orphaned. We manage the process group and
	// shutdown sequence ourselves instead.
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = buildEnv(sc.EnvVars, sc.EnvFrom)
	cmd.Dir = sc.WorkDir
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		result.Result = ext.ResultFailed
		result.Error = fmt.Sprintf("failed to start scenario process: %v", err)
		result.Output = out.String()
		return result
	}

	waitErr := waitWithGracefulCancel(runCtx, cmd, chaosShutdownGracePeriod)
	result.Output = out.String()

	if waitErr != nil {
		result.Result = ext.ResultFailed
		switch {
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			result.Error = fmt.Sprintf("scenario timed out after %s: %v", timeout, waitErr)
		case errors.Is(runCtx.Err(), context.Canceled):
			result.Error = fmt.Sprintf("scenario was cancelled: %v", waitErr)
		default:
			result.Error = fmt.Sprintf("scenario exited with error: %v", waitErr)
		}
		return result
	}

	result.Result = ext.ResultPassed
	return result
}

// resolveCommand returns argv for exec.Command. Entrypoint wins; otherwise
// ScriptPath is extracted and wrapped as `python3 <tmp>`.
func resolveCommand(sc ScenarioSpec, scriptFS fs.FS) (argv []string, cleanup func(), err error) {
	noop := func() {}
	if sc.Entrypoint != "" {
		return append([]string{sc.Entrypoint}, sc.Args...), noop, nil
	}
	scriptPath, cleanup, err := extractScript(scriptFS, sc.ScriptPath)
	if err != nil {
		return nil, noop, fmt.Errorf("failed to extract scenario script %q: %v", sc.ScriptPath, err)
	}
	return []string{"python3", scriptPath}, cleanup, nil
}

// waitWithGracefulCancel waits for cmd to finish. If ctx is done first, it
// signals cmd's entire process group with SIGTERM, waits up to gracePeriod
// for it to exit on its own, and escalates to SIGKILL if it hasn't.
func waitWithGracefulCancel(ctx context.Context, cmd *exec.Cmd, gracePeriod time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		signalProcessGroup(cmd, syscall.SIGTERM)
		select {
		case err := <-done:
			return err
		case <-time.After(gracePeriod):
			signalProcessGroup(cmd, syscall.SIGKILL)
			return <-done
		}
	}
}

// signalProcessGroup signals the process group led by cmd (negative PID),
// which reaches any child processes the scenario itself spawned (kubectl,
// oc, etc.), not just the python3 process directly.
func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}

// boundedBuffer is an io.Writer that keeps only the last maxBytes written to
// it, to protect against unbounded memory growth from chatty subprocesses.
// It is safe for concurrent use since cmd.Stdout/Stderr may be written from
// different goroutines by os/exec.
type boundedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	maxBytes  int
	truncated bool
}

func newBoundedBuffer(maxBytes int) *boundedBuffer {
	return &boundedBuffer{maxBytes: maxBytes}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n := len(p)
	b.buf.Write(p)

	if excess := b.buf.Len() - b.maxBytes; excess > 0 {
		b.buf.Next(excess) // drop oldest bytes, keep the tail
		b.truncated = true
	}

	return n, nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.truncated {
		return fmt.Sprintf("... [output truncated to last %d bytes] ...\n%s", b.maxBytes, b.buf.String())
	}
	return b.buf.String()
}

// extractScript reads a file from scriptFS and writes it to a temporary file
// on disk, returning its path.  The caller must invoke the returned cleanup
// function (typically via defer) to remove the file.
func extractScript(scriptFS fs.FS, path string) (tmpPath string, cleanup func(), err error) {
	src, err := scriptFS.Open(path)
	if err != nil {
		return "", func() {}, fmt.Errorf("open %q in embedded FS: %w", path, err)
	}
	defer src.Close()

	tmp, err := os.CreateTemp("", "krkn-scenario-*.py")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp file: %w", err)
	}

	if _, err = io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", func() {}, fmt.Errorf("write temp file: %w", err)
	}

	// python3 reads the script as an argument; the file only needs to be
	// readable by its owner, not executable (it's never invoked directly).
	if err = tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", func() {}, fmt.Errorf("chmod temp file: %w", err)
	}

	if err = tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", func() {}, fmt.Errorf("close temp file: %w", err)
	}

	return tmp.Name(), func() { _ = os.Remove(tmp.Name()) }, nil
}

// buildEnv constructs the environment for the subprocess as a deduplicated
// key=value list. Order of precedence (highest last):
//  1. ambient process environment (kubeconfig, secret-derived vars, job env)
//  2. EnvFrom remaps (copy ambient KEY onto another name)
//  3. KRKN_KUBE_CONFIG ← KUBECONFIG when neither ambient nor overrides set it
//  4. per-scenario EnvVars overrides
func buildEnv(overrides, envFrom map[string]string) []string {
	env := make(map[string]string)

	for _, kv := range os.Environ() {
		if k, v, ok := splitEnv(kv); ok {
			env[k] = v
		}
	}

	for dest, src := range envFrom {
		if dest == "" || src == "" {
			continue
		}
		if v, ok := env[src]; ok && v != "" {
			env[dest] = v
		}
	}

	// Expose KUBECONFIG under the name krkn-lib-kubernetes expects, unless
	// it's already set (ambiently, via EnvFrom, or via an explicit override).
	if _, ambientlySet := env["KRKN_KUBE_CONFIG"]; !ambientlySet {
		if _, overridden := overrides["KRKN_KUBE_CONFIG"]; !overridden {
			if kubeconfig := env["KUBECONFIG"]; kubeconfig != "" {
				env["KRKN_KUBE_CONFIG"] = kubeconfig
			}
		}
	}

	for k, v := range overrides {
		env[k] = v
	}

	result := make([]string, 0, len(env))
	for k, v := range env {
		result = append(result, k+"="+v)
	}
	return result
}

// splitEnv splits a "KEY=VALUE" string from os.Environ() into its parts.
func splitEnv(kv string) (key, value string, ok bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return "", "", false
}
