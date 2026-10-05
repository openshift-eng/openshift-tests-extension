package ginkgo

import (
	"bytes"
	"testing"
	"time"

	"github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
)

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "clean JSON object",
			input: `{"name": "test", "result": "passed"}`,
			want:  `{"name": "test", "result": "passed"}`,
		},
		{
			name:  "clean JSON array",
			input: `[{"name": "test", "result": "passed"}]`,
			want:  `[{"name": "test", "result": "passed"}]`,
		},
		{
			name:  "klog INFO lines before JSON object",
			input: "I0625 19:25:00.000000   12345 client.go:123] Connecting to server\nI0625 19:25:01.000000   12345 client.go:456] Connected\n{\"name\": \"test\", \"result\": \"passed\"}\n",
			want:  `{"name": "test", "result": "passed"}`,
		},
		{
			name:  "klog WARNING lines before JSON array",
			input: "W0625 19:25:00.000000   12345 controller.go:47] some warning\n[{\"name\": \"test\", \"result\": \"passed\"}]\n",
			want:  `[{"name": "test", "result": "passed"}]`,
		},
		{
			name:  "Ginkgo reporter output before JSON",
			input: "FAIL!\nRan 1 of 1 Specs in 5.123 seconds\n1 Passed | 0 Failed | 0 Pending | 0 Skipped\n{\"name\": \"test\", \"result\": \"failed\"}\n",
			want:  `{"name": "test", "result": "failed"}`,
		},
		{
			name:  "trailing non-JSON after JSON payload",
			input: "{\"name\": \"test\", \"result\": \"passed\"}\nFAIL!\nRan 1 of 1 Specs\n",
			want:  `{"name": "test", "result": "passed"}`,
		},
		{
			name:  "klog lines before AND Ginkgo summary after JSON",
			input: "I0625 19:25:00.000000   12345 client.go:123] Starting\n[{\"name\": \"test\", \"result\": \"passed\"}]\nRan 1 of 1 Specs in 2.000 seconds\nSUCCESS!\n",
			want:  `[{"name": "test", "result": "passed"}]`,
		},
		{
			name:    "no JSON in output - only log lines",
			input:   "I0625 19:25:00.000000   12345 client.go:123] Connecting\nW0625 some warning\n",
			wantErr: true,
		},
		{
			name:    "empty output",
			input:   "",
			wantErr: true,
		},
		{
			name:    "only whitespace and newlines",
			input:   "  \n\n  \n",
			wantErr: true,
		},
		{
			name:  "JSON with leading whitespace on the line",
			input: "I0625 log line\n  {\"name\": \"test\", \"result\": \"passed\"}\n",
			want:  `{"name": "test", "result": "passed"}`,
		},
		{
			name:  "multiline pretty-printed JSON object",
			input: "I0625 log line\n{\n  \"name\": \"test\",\n  \"result\": \"passed\"\n}\n",
			want:  "{\n  \"name\": \"test\",\n  \"result\": \"passed\"\n}",
		},
		{
			name:  "JSON with trailing whitespace and CRLF",
			input: "{\"name\": \"test\", \"result\": \"passed\"}\r\n",
			want:  `{"name": "test", "result": "passed"}`,
		},
		{
			name:  "non-JSON bracket lines before valid JSON array",
			input: "[FAILED]\n[BeforeEach]\n[{\"name\": \"test\", \"result\": \"passed\"}]\n",
			want:  `[{"name": "test", "result": "passed"}]`,
		},
		{
			name:  "non-JSON brace lines before valid JSON object",
			input: "{not-json}\n{also not json\n{\"name\": \"test\", \"result\": \"passed\"}\n",
			want:  `{"name": "test", "result": "passed"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractJSON([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Errorf("extractJSON() expected error, got nil with result: %s", string(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("extractJSON() unexpected error: %v", err)
			}

			// Compare as raw JSON strings (byte-level, since we're testing extraction boundaries)
			if string(got) != tt.want {
				t.Errorf("extractJSON() =\n  %s\nwant:\n  %s", string(got), tt.want)
			}
		})
	}
}

// TestIntegrationStdoutContamination tests the full newTestResultFromOutput parsing
// pipeline with realistic stdout contamination from Ginkgo reporter output, klog
// lines, and other non-JSON content that extension binaries commonly emit.
func TestIntegrationStdoutContamination(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantName   string
		wantResult string
		wantErr    bool
	}{
		{
			name: "realistic Ginkgo pass with klog and reporter output before JSON object",
			input: "I0625 19:25:00.000000   12345 client.go:123] Connecting to apiserver...\n" +
				"I0625 19:25:01.000000   12345 client.go:456] Connected successfully\n" +
				"W0625 19:25:01.500000   12345 deprecation.go:78] API v1beta1 is deprecated\n" +
				"[BeforeEach] [sig-apps] Deployment should run the lifecycle of a Deployment\n" +
				"  /go/src/test/e2e/apps/deployment.go:45\n" +
				"[It] [sig-apps] Deployment should run the lifecycle of a Deployment\n" +
				"  /go/src/test/e2e/apps/deployment.go:50\n" +
				"STEP: Creating a deployment\n" +
				"STEP: Waiting for deployment to complete\n" +
				"{\"name\":\"[sig-apps] Deployment should run the lifecycle of a Deployment\",\"lifecycle\":\"\",\"duration\":5123000000,\"startTime\":\"2025-06-25 19:25:00.000000 UTC\",\"endTime\":\"2025-06-25 19:25:05.000000 UTC\",\"result\":\"passed\",\"output\":\"deployment created successfully\"}\n" +
				"Ran 1 of 1 Specs in 5.123 seconds\n" +
				"SUCCESS! -- 1 Passed | 0 Failed | 0 Pending | 0 Skipped\n",
			wantName:   "[sig-apps] Deployment should run the lifecycle of a Deployment",
			wantResult: "passed",
		},
		{
			name: "realistic Ginkgo failure with FAILED markers and klog before JSON array",
			input: "I0625 19:30:00.000000   67890 setup.go:99] Initializing test framework\n" +
				"[BeforeEach] [sig-network] Services should provide secure master service\n" +
				"  /go/src/test/e2e/network/service.go:100\n" +
				"[FAILED] [sig-network] Services should provide secure master service\n" +
				"  Expected success, but got an error:\n" +
				"      <*errors.StatusError | 0xc000abcdef>: connection refused\n" +
				"FAIL! -- 0 Passed | 1 Failed | 0 Pending | 0 Skipped\n" +
				"[{\"name\":\"[sig-network] Services should provide secure master service\",\"lifecycle\":\"\",\"duration\":30000000000,\"startTime\":\"2025-06-25 19:30:00.000000 UTC\",\"endTime\":\"2025-06-25 19:30:30.000000 UTC\",\"result\":\"failed\",\"output\":\"connection refused\",\"error\":\"timed out waiting for service\"}]\n" +
				"Ran 1 of 1 Specs in 30.000 seconds\n",
			wantName:   "[sig-network] Services should provide secure master service",
			wantResult: "failed",
		},
		{
			name: "heavy klog contamination with CRLF line endings and JSON object",
			input: "I0625 19:40:00.000000   11111 reflector.go:219] Listing and watching\r\n" +
				"I0625 19:40:00.100000   11111 reflector.go:255] Caches populated\r\n" +
				"I0625 19:40:00.200000   11111 shared_informer.go:270] Synced\r\n" +
				"E0625 19:40:01.000000   11111 leaderelection.go:330] error retrieving lock\r\n" +
				"{\"name\":\"[sig-auth] RBAC should handle basic RBAC operations\",\"lifecycle\":\"\",\"duration\":2000000000,\"startTime\":\"2025-06-25 19:40:00.000000 UTC\",\"endTime\":\"2025-06-25 19:40:02.000000 UTC\",\"result\":\"passed\",\"output\":\"rbac verified\"}\r\n",
			wantName:   "[sig-auth] RBAC should handle basic RBAC operations",
			wantResult: "passed",
		},
		{
			name: "Ginkgo spec output with multiple bracket-prefixed non-JSON lines before JSON",
			input: "[BeforeEach] setup cluster\n" +
				"[JustBeforeEach] configure workload\n" +
				"[It] [sig-storage] PV should be bound\n" +
				"[AfterEach] teardown\n" +
				"[FAILED] in [It] - /go/src/test.go:42\n" +
				"[{\"name\":\"[sig-storage] PV should be bound\",\"lifecycle\":\"\",\"duration\":10000000000,\"startTime\":\"2025-06-25 19:45:00.000000 UTC\",\"endTime\":\"2025-06-25 19:45:10.000000 UTC\",\"result\":\"failed\",\"output\":\"pv not bound\",\"error\":\"timeout\"}]\n",
			wantName:   "[sig-storage] PV should be bound",
			wantResult: "failed",
		},
		{
			name: "only Ginkgo noise with no valid JSON anywhere",
			input: "I0625 19:50:00.000000   22222 client.go:100] Starting\n" +
				"[BeforeEach] some test\n" +
				"[FAILED] some test\n" +
				"FAIL! -- 0 Passed | 1 Failed\n" +
				"{not valid json at all}\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := bytes.NewBufferString(tt.input)
			result, err := newTestResultFromOutput(buf)
			if tt.wantErr {
				if err == nil {
					t.Errorf("newTestResultFromOutput() expected error, got result: %+v", result)
				}
				return
			}
			if err != nil {
				t.Fatalf("newTestResultFromOutput() unexpected error: %v", err)
			}
			if result.Name != tt.wantName {
				t.Errorf("result.Name = %q, want %q", result.Name, tt.wantName)
			}
			if string(result.Result) != tt.wantResult {
				t.Errorf("result.Result = %q, want %q", result.Result, tt.wantResult)
			}
		})
	}
}

func TestNewTestResultFromOutput(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantName   string
		wantResult string
		wantErr    bool
	}{
		{
			name:       "clean JSON object",
			input:      `{"name": "my-test", "result": "passed", "duration": 1000}`,
			wantName:   "my-test",
			wantResult: "passed",
		},
		{
			name:       "clean JSON array with one element",
			input:      `[{"name": "my-test", "result": "failed", "duration": 2000}]`,
			wantName:   "my-test",
			wantResult: "failed",
		},
		{
			name:       "klog lines before JSON object",
			input:      "I0625 19:25:00.000000   12345 client.go:123] Connecting\n{\"name\": \"my-test\", \"result\": \"passed\", \"duration\": 500}\n",
			wantName:   "my-test",
			wantResult: "passed",
		},
		{
			name:       "klog lines before JSON array",
			input:      "I0625 19:25:00.000000   12345 setup.go:99] Initializing\nW0625 19:25:01.000000   12345 warn.go:10] Deprecation\n[{\"name\": \"my-test\", \"result\": \"passed\", \"duration\": 100}]\n",
			wantName:   "my-test",
			wantResult: "passed",
		},
		{
			name:       "Ginkgo reporter output surrounding JSON",
			input:      "FAIL!\nRan 1 of 1 Specs in 5.123 seconds\n{\"name\": \"my-test\", \"result\": \"failed\", \"duration\": 5123}\nSUCCESS! -- 1 Passed | 0 Failed\n",
			wantName:   "my-test",
			wantResult: "failed",
		},
		{
			name:    "empty output",
			input:   "",
			wantErr: true,
		},
		{
			name:    "no JSON at all",
			input:   "I0625 just log lines\nno json here\n",
			wantErr: true,
		},
		{
			name:    "JSON array with zero elements",
			input:   `[]`,
			wantErr: true,
		},
		{
			name:    "JSON array with two elements",
			input:   `[{"name": "a", "result": "passed"},{"name": "b", "result": "failed"}]`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := bytes.NewBufferString(tt.input)
			result, err := newTestResultFromOutput(buf)
			if tt.wantErr {
				if err == nil {
					t.Errorf("newTestResultFromOutput() expected error, got result: %+v", result)
				}
				return
			}
			if err != nil {
				t.Fatalf("newTestResultFromOutput() unexpected error: %v", err)
			}
			if result.Name != tt.wantName {
				t.Errorf("result.Name = %q, want %q", result.Name, tt.wantName)
			}
			if string(result.Result) != tt.wantResult {
				t.Errorf("result.Result = %q, want %q", result.Result, tt.wantResult)
			}
		})
	}
}

// TestNewTestResultFromOutput_StdoutPollution documents the root cause of the panic:
// when a subprocess emits an unrelated JSON object to stdout before the actual test
// result (e.g. an oc or loki API response), extractJSON picks up the wrong JSON first,
// returning a parsed ExtensionTestResult with Result == "".
// SpawnProcessToRunTest must detect this and fall back to newTestResult.
func TestNewTestResultFromOutput_StdoutPollution(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantResult string
	}{
		{
			name: "unrelated JSON object before actual test result picks up wrong JSON",
			// First JSON line is an unrelated object (e.g. an API response); extractJSON
			// picks it up before the real result, yielding Result == "".
			input: `{"kind":"PodList","apiVersion":"v1","items":[]}` + "\n" +
				`{"name":"[sig-apps] example test should succeed","result":"passed","duration":10000}` + "\n",
			wantResult: "", // Result is empty — the unrelated JSON was parsed
		},
		{
			name:       "JSON with explicitly empty result field",
			input:      `{"name":"some-test","result":"","duration":1000}`,
			wantResult: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := bytes.NewBufferString(tt.input)
			result, err := newTestResultFromOutput(buf)
			if err != nil {
				t.Fatalf("newTestResultFromOutput() unexpected error: %v", err)
			}
			if string(result.Result) != tt.wantResult {
				t.Errorf("result.Result = %q, want %q", result.Result, tt.wantResult)
			}
		})
	}
}

// TestHandleSubprocessResult exercises the invalid-result branch in handleSubprocessResult,
// which is the path SpawnProcessToRunTest takes when stdout pollution causes extractJSON to
// parse the wrong JSON and return Result == "". Verifies the fallback produces a failed
// result with the correct test name, timing, and diagnostic captured in Error.
func TestHandleSubprocessResult(t *testing.T) {
	t.Run("known result types are returned unchanged", func(t *testing.T) {
		for _, r := range []extensiontests.Result{extensiontests.ResultPassed, extensiontests.ResultFailed, extensiontests.ResultSkipped} {
			parsed := &extensiontests.ExtensionTestResult{Name: "original-name", Result: r}
			got := handleSubprocessResult(parsed, "requested-name", time.Time{}, time.Time{}, &bytes.Buffer{}, &bytes.Buffer{})
			if got != parsed {
				t.Errorf("result %q: expected same pointer back, got a different result", r)
			}
		}
	})

	t.Run("unknown result falls back with correct name, timing, and diagnostic", func(t *testing.T) {
		requestedName := "[sig-apps] example test should succeed"
		start := time.Now().Add(-5 * time.Second)
		end := time.Now()
		stdout := bytes.NewBufferString(`{"kind":"PodList","apiVersion":"v1","items":[]}` + "\n")
		stderr := &bytes.Buffer{}

		parsed := &extensiontests.ExtensionTestResult{Name: "wrong-name-from-polluted-json", Result: extensiontests.Result("")}
		result := handleSubprocessResult(parsed, requestedName, start, end, stdout, stderr)

		if result.Name != requestedName {
			t.Errorf("result.Name = %q, want %q", result.Name, requestedName)
		}
		if result.Result != extensiontests.ResultFailed {
			t.Errorf("result.Result = %q, want %q", result.Result, extensiontests.ResultFailed)
		}
		if result.Error == "" {
			t.Error("result.Error is empty; expected stderr diagnostic to be captured")
		}
		wantDiag := "invalid result type"
		if !bytes.Contains([]byte(result.Error), []byte(wantDiag)) {
			t.Errorf("result.Error = %q, want it to contain %q", result.Error, wantDiag)
		}
		if result.StartTime == nil || result.EndTime == nil {
			t.Error("result.StartTime or EndTime is nil; timing was not preserved")
		}
	})
}
