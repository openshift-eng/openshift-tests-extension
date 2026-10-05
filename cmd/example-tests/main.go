package main

import (
	"fmt"
	"os"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	// If using ginkgo, import your tests here
	_ "github.com/openshift-eng/openshift-tests-extension/test/example"
)

func main() {
	// When this binary is spawned as a subprocess by SpawnProcessToRunTest
	// (i.e. `run-test` is the first argument), emit realistic non-JSON content
	// to stdout before the cobra command runs. This simulates the kind of
	// stdout contamination that occurs in practice from klog, Ginkgo reporters,
	// and other libraries that write to stdout.
	if len(os.Args) > 1 && os.Args[1] == "run-test" {
		// writeNoise emits a simulated log line to stdout. Errors are
		// intentionally discarded — this is deliberate test noise and an
		// early stdout closure is harmless.
		writeNoise := func(line string) {
			_, _ = fmt.Fprintln(os.Stdout, line)
		}
		writeNoise(`I0625 19:25:00.000000   12345 client.go:123] Connecting to apiserver...`)
		writeNoise(`W0625 19:25:01.000000   12345 deprecation.go:78] API v1beta1 is deprecated, use v1`)
		writeNoise(`I0625 19:25:01.100000   12345 round_trippers.go:466] GET https://api.example.com:6443/api 200 OK`)
		writeNoise(`[BeforeEach] [sig-testing] openshift-tests-extension`)
		writeNoise(`  /go/src/github.com/openshift-eng/openshift-tests-extension/test/example/example.go:28`)
		writeNoise(`[It] [sig-testing] openshift-tests-extension should support passing tests`)
		writeNoise(`STEP: Setting up test environment`)
	}

	// Extension registry
	registry := e.NewRegistry()

	// You can declare multiple extensions, but most people will probably only need to create one.
	ext := e.NewExtension("openshift", "payload", "example-tests")

	// Add suites to the extension. Specifying parents will cause the tests from this suite
	// to be included when a parent is invoked.
	ext.AddSuite(
		e.Suite{
			Name:    "example/tests",
			Parents: []string{"openshift/conformance/parallel"},
		})

	// The tests that a suite is composed of can be filtered by CEL expressions. By
	// default, the qualifiers only apply to tests from this extension.
	ext.AddSuite(e.Suite{
		Name: "example/fast",
		Qualifiers: []string{
			`!labels.exists(l, l=="SLOW")`,
		},
	})

	// Global suites' qualifiers will apply to all tests available, even
	// those outside of this extension (when invoked by origin).
	ext.AddGlobalSuite(e.Suite{
		Name: "example/slow",
		Qualifiers: []string{
			`labels.exists(l, l=="SLOW")`,
		},
	})

	// If using Ginkgo, build test specs automatically
	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		panic(fmt.Sprintf("couldn't build extension test specs from ginkgo: %+v", err.Error()))
	}

	// Environment selector information can be added to test specs to support filtering by environment
	specs.Select(et.NameContains("[sig-testing] openshift-tests-extension should support test-skips via environment flags")).
		Include(et.PlatformEquals("aws"))

	// You can add hooks to run before/after tests. There are BeforeAll, BeforeSpawn,
	// BeforeEach, AfterEach, and AfterAll. "Each" and "Spawn" functions must be thread safe.
	//
	// specs.AddBeforeAll(func() {
	// 	initializeTestFramework()
	// })
	//
	// BeforeSpawn runs before RunParallel, not for in-process Run. Ginkgo runners
	// pass Env to the child automatically.
	//
	// specs.AddBeforeSpawn(func(name string, options *et.SpawnOptions) {
	// 	options.Env = map[string]string{"SLOT": allocateSlot(name)}
	// })
	//
	// specs.AddBeforeEach(func(spec et.ExtensionTestSpec) {
	//	if spec.Name == "my test" {
	//		// do stuff
	//	}
	// })
	//
	// specs.AddAfterEach(func(res *et.ExtensionTestResult) {
	// 	if res.Result == et.ResultFailed && apiTimeoutRegexp.Matches(res.Output) {
	// 		res.AddDetails("api-timeout", collectDiagnosticInfo())
	// 	}
	// })

	// You can also manually build a test specs list from other testing tooling
	// TODO: example

	// Modify specs, such as adding a label to all specs
	// 	specs = specs.AddLabel("SLOW")

	// Specs can be globally filtered...
	// specs = specs.MustFilter([]string{`name.contains("filter")`})

	// Or walked...
	// specs = specs.Walk(func(spec *extensiontests.ExtensionTestSpec) {
	//	if strings.Contains(e.Name, "scale up") {
	//		e.Labels.Insert("SLOW")
	//	}
	//
	// Specs can also be selected...
	// specs = specs.Select(et.NameContains("slow test")).AddLabel("SLOW")
	//
	// Or with "any" (or) matching selections
	// specs = specs.SelectAny(et.NameContains("slow test"), et.HasLabel("SLOW"))
	//
	// Or with "all" (and) matching selections
	// specs = specs.SelectAll(et.NameContains("slow test"), et.HasTagWithValue("speed", "slow"))
	//
	// There are also Must* functions for any of the above flavors of selection
	// which will return an error if nothing is found
	// specs, err = specs.MustSelect(et.NameContains("slow test")).AddLabel("SLOW")
	// if err != nil {
	//    logrus.Warn("no specs found: %w", err)
	// }
	// Test renames
	//	if spec.Name == "[sig-testing] openshift-tests-extension has a test with a typo" {
	//		spec.OriginalName = `[sig-testing] openshift-tests-extension has a test with a tpyo`
	//	}
	//
	// Filter by environment flags
	// if spec.Name == "[sig-testing] openshift-tests-extension should support defining the platform for tests" {
	//		spec.Include(et.PlatformEquals("aws"))
	//		spec.Exclude(et.And(et.NetworkEquals("ovn"), et.TopologyEquals("ha")))
	//	}
	// })

	ext.AddSpecs(specs)
	registry.Register(ext)

	root := &cobra.Command{
		Long: "OpenShift Tests Extension Example",
	}

	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)

	if err := func() error {
		return root.Execute()
	}(); err != nil {
		os.Exit(1)
	}
}
