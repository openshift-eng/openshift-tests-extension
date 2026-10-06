// Package krkn is a prototype of the generic Krkn exec adapter.
//
// The local production copy is /home/oharan/chaos-tests-extension (package
// adapter). GitHub redhat-chaos/chaos-tests-extension remains unpublished
// (404). Do not treat this package as the production adapter.
//
// ScriptPath (extract + python3) remains prototype-only; production specs
// use Entrypoint (krkn-hub prow_run.sh already on the runner image).
package krkn

// ScenarioSpec is the only per-scenario input the adapter understands.
//
// The adapter is scenario-agnostic: it must not branch on Name, Tags, or any
// chaos plugin type (etcd vs node-stop vs hog vs …). Adding a new Krkn
// scenario — including ones outside the first-slice OCP jobs — is a new JSON
// object in the registry, not a Go change.
//
// Krkn-hub already configures every plugin through environment variables
// (prow_run.sh → envsubst → run_kraken.py --config). This struct is that
// same contract plus the few OTE fields the scheduler needs.
type ScenarioSpec struct {
	// Name is the full OTE test name, e.g. "[sig-chaos] etcd pod disruption".
	Name string `json:"name"`

	// Entrypoint is the program to exec. Production specs use "/bin/bash" with
	// Args pointing at ote_wrapper.sh then ./<dir>/prow_run.sh. Preferred over
	// ScriptPath. The file is expected to already exist on the runner image.
	Entrypoint string `json:"entrypoint,omitempty"`

	// Args are extra argv passed to Entrypoint. Production specs set
	// ["/home/krkn/krkn-hub/ote_wrapper.sh", "./<dir>/prow_run.sh"] when
	// Entrypoint is "/bin/bash".
	Args []string `json:"args,omitempty"`

	// WorkDir is an optional chdir before exec. Production specs use
	// "/home/krkn/krkn-hub".
	WorkDir string `json:"workDir,omitempty"`

	// ScriptPath is the prototype path to an embedded file extracted and run
	// as `python3 <tmp>`. Production specs should set Entrypoint instead.
	// Kept so existing unit tests and the in-repo prototype keep working.
	ScriptPath string `json:"scriptPath,omitempty"`

	// Tags become ExtensionTestSpec.Labels (Cypress convention). Use them for
	// suite membership ("pod" / "node") and filters ("SLOW", "DISRUPTIVE") —
	// not for adapter behavior.
	Tags []string `json:"tags,omitempty"`

	// EnvVars is the scenario parameter surface. Plugin choice, namespace,
	// label selectors, durations, CLOUD_TYPE, HEALTH_CHECK_EXIT_ON_FAILURE,
	// telemetry tags, etc. all live here. Values override ambient env.
	// Do not "normalize" strings (e.g. LABEL_SELECTOR trailing '=') — copy
	// production refs verbatim.
	EnvVars map[string]string `json:"envVars,omitempty"`

	// EnvFrom copies an ambient env key onto another name before EnvVars
	// apply. Example: {"TIMEOUT": "POWER_OUTAGE_TIMEOUT"} lets a power-outage
	// spec consume the job's POWER_OUTAGE_TIMEOUT without the adapter knowing
	// what "power outage" is. Missing ambient keys are skipped. EnvVars still
	// win on conflict.
	EnvFrom map[string]string `json:"envFrom,omitempty"`

	// IncludeCEL / ExcludeCEL restrict when the spec is selected (cluster
	// environment). Example: `platform == "aws"` for cloud-API scenarios.
	IncludeCEL string `json:"includeCEL,omitempty"`
	ExcludeCEL string `json:"excludeCEL,omitempty"`

	// Informing marks lifecycle=informing so failures do not fail the job.
	// Newly onboarded scenarios start informing and promote to blocking later.
	Informing bool `json:"informing,omitempty"`

	// Timeout is the adapter-enforced deadline (time.ParseDuration, e.g. "45m").
	// Size it to action + WAIT_DURATION + recovery + cloud-API slop for that
	// spec. Empty/invalid → chaosDefaultTimeout. Do not encode a per-plugin
	// timeout table in Go.
	Timeout string `json:"timeout,omitempty"`
}
