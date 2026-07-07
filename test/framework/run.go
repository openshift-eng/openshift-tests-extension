package framework

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift-eng/openshift-tests-extension/pkg/dbtime"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
)

var _ = Describe("[sig-testing] example-tests run-suite", Label("framework"), func() {
	var result e.ExtensionTestResults
	var output []byte
	var cmdErr error

	BeforeEach(func() {
		cmd := exec.Command("./example-tests", "run-suite", "example/fast")

		// Capture both stdout and stderr
		output, cmdErr = cmd.Output()

		// Expect command to exit with a non-zero status (exit code 1 for failed tests)
		var exitErr *exec.ExitError
		ok := errors.As(cmdErr, &exitErr)
		Expect(ok).To(BeTrue(), "Expected command to exit with a non-zero status")
		Expect(exitErr.ExitCode()).To(Equal(1), "Expected exit code 1")

		// Unmarshal the JSON output into the predefined ExtensionTestResults type
		err := json.Unmarshal(output, &result)
		Expect(err).ShouldNot(HaveOccurred(), "Expected JSON output to unmarshal into ExtensionTestResults")
	})

	It("should contain a test that passed", func() {
		var foundPassed bool
		for _, test := range result {
			if test.Result == "passed" {
				foundPassed = true
				break
			}
		}
		Expect(foundPassed).To(BeTrue(), "Expected at least one test to have passed")
	})

	It("should have the correct error message for the failed test", func() {
		for _, test := range result {
			if test.Name == "[sig-testing] openshift-tests-extension should support panicking tests" && test.Result == "failed" {
				Expect(test.Error).To(ContainSubstring("Test Panicked: oh no"), "Expected error to contain 'Test Panicked: oh no'")
				break
			}
		}
	})

	It("should contain a test that was skipped due to pending", func() {
		var foundPending bool
		for _, test := range result {
			if test.Name == "[sig-testing] openshift-tests-extension should support pending tests" && test.Result == "skipped" {
				foundPending = true
				break
			}
		}
		Expect(foundPending).To(BeTrue(), "Expected pending test (XIt) to be reported as skipped")
	})

	It("fast suite should not have a slow test", func() {
		foundTest := false
		for _, test := range result {
			if test.Name == "[sig-testing] openshift-tests-extension should support slow tests" {
				foundTest = true
				break
			}
		}
		Expect(foundTest).To(BeFalse(), "Expected to not find a slow test")
	})

	/*It("slow suite should contain a slow test", func() {
		foundTest := false
		for _, test := range result {
			if test.Name == "[sig-testing] openshift-tests-extension should support slow tests" {
				foundTest = true
				Expect(test.Duration).To(BeNumerically(">=", 15000), "Expected slow test to take at least 15 seconds")
				break
			}
		}
		Expect(foundTest).To(BeTrue(), "Expected to find a slow test")
	})*/
})

var _ = Describe("[sig-testing] example-tests run-suite pool scheduling", Label("framework"), func() {
	var results e.ExtensionTestResults
	var stderrStr string

	BeforeEach(func() {
		cmd := exec.Command("./example-tests", "run-suite", "example/pools")

		var stderr strings.Builder
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		Expect(err).ShouldNot(HaveOccurred(), "Expected pool suite to exit 0 (all tests pass)")

		Expect(json.Unmarshal(output, &results)).Should(Succeed())
		stderrStr = stderr.String()

		GinkgoWriter.Println(renderTimeline(results))
	})

	It("should complete all tests", func() {
		Expect(results).To(HaveLen(13), "Expected 13 pool tests (8 worker + 2 exclusive + 3 no-pool)")

		for _, r := range results {
			Expect(r.Result).To(Equal(e.Result("passed")),
				"Expected test %q to pass, got %q", r.Name, r.Result)
		}
	})

	It("should emit pool dispatch and complete log lines", func() {
		Expect(stderrStr).To(ContainSubstring("[scheduler] dispatch"),
			"Expected pool dispatch log lines on stderr")
		Expect(stderrStr).To(ContainSubstring("[scheduler] complete"),
			"Expected pool complete log lines on stderr")
	})

	It("should never exceed pool capacity of 2 among pool-demanding tests", func() {
		var poolResults e.ExtensionTestResults
		for _, r := range results {
			if poolDemand(r) > 0 {
				poolResults = append(poolResults, r)
			}
		}
		Expect(peakConcurrency(poolResults)).To(BeNumerically("<=", 2),
			"Pool capacity is 2, so at most 2 pool-demanding tests should run concurrently")
	})

	It("should run each exclusive test with no overlap against other pool-demanding tests", func() {
		var exclusives, poolDemanding e.ExtensionTestResults
		for _, r := range results {
			d := poolDemand(r)
			if d >= 2 {
				exclusives = append(exclusives, r)
			}
			if d > 0 {
				poolDemanding = append(poolDemanding, r)
			}
		}
		Expect(exclusives).To(HaveLen(2), "Expected 2 exclusive tests (demand >= 2)")

		for _, exc := range exclusives {
			for _, other := range poolDemanding {
				if other.Name == exc.Name {
					continue
				}
				Expect(overlaps(exc, other)).To(BeFalse(),
					"Exclusive test %q (demands %d workers) overlapped with %q",
					exc.Name, poolDemand(exc), other.Name)
			}
		}
	})

	It("should run non-pool tests without being blocked by pool constraints", func() {
		var noPool e.ExtensionTestResults
		for _, r := range results {
			if poolDemand(r) == 0 {
				noPool = append(noPool, r)
			}
		}
		Expect(noPool).To(HaveLen(3), "Expected 3 non-pool tests")

		for _, np := range noPool {
			overlappedSomething := false
			for _, other := range results {
				if other.Name == np.Name {
					continue
				}
				if overlaps(np, other) {
					overlappedSomething = true
					break
				}
			}
			Expect(overlappedSomething).To(BeTrue(),
				"Non-pool test %q should have overlapped with at least one other test (not serialized)", np.Name)
		}
	})
})

func toTime(dbt *dbtime.DBTime) time.Time {
	return time.Time(*dbt)
}

func overlaps(a, b *e.ExtensionTestResult) bool {
	return toTime(a.StartTime).Before(toTime(b.EndTime)) &&
		toTime(b.StartTime).Before(toTime(a.EndTime))
}

func peakConcurrency(results e.ExtensionTestResults) int {
	type event struct {
		t     time.Time
		delta int
	}
	var events []event
	for _, r := range results {
		events = append(events,
			event{toTime(r.StartTime), 1},
			event{toTime(r.EndTime), -1},
		)
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].t.Equal(events[j].t) {
			return events[i].delta < events[j].delta // ends before starts at same instant
		}
		return events[i].t.Before(events[j].t)
	})
	peak, running := 0, 0
	for _, ev := range events {
		running += ev.delta
		if running > peak {
			peak = running
		}
	}
	return peak
}

func poolDemand(r *e.ExtensionTestResult) int {
	total := 0
	for _, v := range r.ResourcePools {
		total += v
	}
	return total
}

// renderTimeline produces an ASCII interval chart showing when each test was running.
// Each row is a test, and the horizontal axis is time quantized into columns.
// Example:
//
//	Pool Scheduling Timeline (capacity: 2)
//	 0ms                                            150ms
//	 |                                                 |
//	 worker-test-0        [████████████]
//	 worker-test-1        [████████████]
//	 worker-test-2                      [████████████]
//	 worker-test-3                      [████████████]
//	 exclusive-worker-test                            [████████████]
//	                       ─── peak concurrency: 2 ───
func renderTimeline(results e.ExtensionTestResults) string {
	if len(results) == 0 {
		return ""
	}

	// Sort by start time, then by name for stable output.
	sorted := make(e.ExtensionTestResults, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool {
		si, sj := toTime(sorted[i].StartTime), toTime(sorted[j].StartTime)
		if si.Equal(sj) {
			return sorted[i].Name < sorted[j].Name
		}
		return si.Before(sj)
	})

	// Find the global time range.
	earliest := toTime(sorted[0].StartTime)
	latest := toTime(sorted[0].EndTime)
	for _, r := range sorted {
		if s := toTime(r.StartTime); s.Before(earliest) {
			earliest = s
		}
		if e := toTime(r.EndTime); e.After(latest) {
			latest = e
		}
	}
	totalDur := latest.Sub(earliest)
	if totalDur == 0 {
		totalDur = time.Millisecond
	}

	const chartWidth = 50

	// Shorten names: strip the common "[sig-testing] pool-scheduling " prefix.
	const namePrefix = "[sig-testing] pool-scheduling "
	shortName := func(name string) string {
		return strings.TrimPrefix(name, namePrefix)
	}

	// Find the longest short name for alignment.
	maxName := 0
	for _, r := range sorted {
		if n := len(shortName(r.Name)); n > maxName {
			maxName = n
		}
	}

	var b strings.Builder
	b.WriteString("\nPool Scheduling Timeline (capacity: 2)\n")

	// Header: time axis.
	endLabel := fmt.Sprintf("%dms", totalDur.Milliseconds())
	pad := maxName + 2
	b.WriteString(fmt.Sprintf("%*s0ms%*s%s\n", pad, "", chartWidth-3-len(endLabel), "", endLabel))
	b.WriteString(fmt.Sprintf("%*s|%*s|\n", pad, "", chartWidth-1, ""))

	// One row per test, with pool demand annotation.
	for _, r := range sorted {
		name := shortName(r.Name)
		startOff := toTime(r.StartTime).Sub(earliest)
		endOff := toTime(r.EndTime).Sub(earliest)

		startCol := int(float64(startOff) / float64(totalDur) * float64(chartWidth))
		endCol := int(float64(endOff) / float64(totalDur) * float64(chartWidth))
		if endCol <= startCol {
			endCol = startCol + 1
		}

		line := make([]byte, chartWidth)
		for i := range line {
			line[i] = ' '
		}
		line[startCol] = '['
		for i := startCol + 1; i < endCol-1; i++ {
			line[i] = '='
		}
		if endCol-1 > startCol {
			line[endCol-1] = ']'
		}

		demand := poolDemand(r)
		demandStr := "(-)"
		if demand > 0 {
			demandStr = fmt.Sprintf("(%d)", demand)
		}

		b.WriteString(fmt.Sprintf("  %-*s %s %s\n", maxName, name, demandStr, string(line)))
	}

	// Footer: peak concurrency.
	peak := peakConcurrency(results)
	footer := fmt.Sprintf("peak concurrency: %d", peak)
	b.WriteString(fmt.Sprintf("%*s%s\n", pad+(chartWidth-len(footer))/2, "", footer))

	return b.String()
}

var _ = Describe("[sig-testing] example-tests HTML output", Label("framework"), func() {
	It("should produce a valid HTML artifact", func() {
		tmpDir, err := os.MkdirTemp("", "html-test")
		Expect(err).ShouldNot(HaveOccurred())
		defer os.RemoveAll(tmpDir)

		htmlPath := filepath.Join(tmpDir, "results.html")
		cmd := exec.Command("./example-tests", "run-suite", "example/fast", "--html-path", htmlPath)
		_, cmdErr := cmd.Output()

		// Command exits with error due to intentionally failing tests, but HTML should still be produced
		var exitErr *exec.ExitError
		ok := errors.As(cmdErr, &exitErr)
		Expect(ok).To(BeTrue(), "Expected command to exit with a non-zero status")

		// Verify HTML file was created
		htmlContent, err := os.ReadFile(htmlPath)
		Expect(err).ShouldNot(HaveOccurred(), "Expected HTML file to be created")
		Expect(len(htmlContent)).To(BeNumerically(">", 0), "Expected HTML file to have content")

		// Verify it contains expected HTML structure
		htmlStr := string(htmlContent)
		Expect(htmlStr).To(ContainSubstring("<!DOCTYPE html>"), "Expected valid HTML doctype")
		Expect(htmlStr).To(ContainSubstring("Results for example/fast"), "Expected suite name in title")
		Expect(htmlStr).To(ContainSubstring("<script id=\"test-data\""), "Expected embedded test data")

		// Verify the embedded JSON is valid by checking it doesn't contain unrendered template
		Expect(htmlStr).NotTo(ContainSubstring("{{ .Data }}"), "Expected template to be rendered")
		Expect(htmlStr).NotTo(ContainSubstring("{{ .SuiteName }}"), "Expected template to be rendered")

		// Verify test data is embedded
		Expect(strings.Count(htmlStr, "sig-testing")).To(BeNumerically(">", 0), "Expected test names in HTML")
	})
})
