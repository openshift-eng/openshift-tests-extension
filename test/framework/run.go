package framework

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
