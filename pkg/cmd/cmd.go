package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"k8s.io/klog/v2"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd/cmdimages"
	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd/cmdinfo"
	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd/cmdlist"
	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd/cmdrun"
	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd/cmdupdate"
	"github.com/openshift-eng/openshift-tests-extension/pkg/extension"
)

func init() {
	// Configure klog to write to stderr instead of stdout BEFORE any commands run.
	// This prevents klog warnings from corrupting JSON output during test listing.
	//
	// Background:
	// - The 'list tests' command outputs JSON to stdout for consumption by CI systems
	// - klog defaults to writing to stdout, which can include warnings from imported packages
	// - When klog warnings appear in stdout, they corrupt the JSON stream with messages like:
	//   "W0327 12:34:56.123456 12345 warnings.go:70] deprecation warning"
	// - This causes JSON parsers to fail with: "invalid character 'W' looking for beginning of value"
	//
	// Solution:
	// - Redirect all klog output to stderr, keeping stdout clean for structured output
	// - This is consistent with how pkg/ginkgo/util.go configures GinkgoWriter to stderr
	// - In CI environments: stdout = structured data (JSON), stderr = logs/diagnostics
	//
	// See: https://github.com/openshift/cluster-authentication-operator/pull/857
	klog.SetOutput(os.Stderr)
}

func DefaultExtensionCommands(registry *extension.Registry) []*cobra.Command {
	return []*cobra.Command{
		cmdrun.NewRunSuiteCommand(registry),
		cmdrun.NewRunTestCommand(registry),
		cmdlist.NewListCommand(registry),
		cmdinfo.NewInfoCommand(registry),
		cmdupdate.NewUpdateCommand(registry),
		cmdimages.NewImagesCommand(registry),
	}
}
