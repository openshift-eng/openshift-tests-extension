package main

//go:generate go run ../../hack/generate-test-registry/main.go -input ../../test/gotest -output ../../test/gotest/generated_tests.go -package gotest

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	gt "github.com/openshift-eng/openshift-tests-extension/pkg/gotest"
	
	// Import the test package to access tests
	tests "github.com/openshift-eng/openshift-tests-extension/test/gotest"
)

// gotest-example demonstrates how to use the OTE wrapper for standard Go tests
func main() {
	registry := e.NewRegistry()
	ext := e.NewExtension("openshift", "gotest", "example")

	ext.AddSuite(e.Suite{
		Name: "gotest/all",
	})

	// Build test specs from Go tests
	// Tests are compiled into the binary, making it self-contained
	specs, err := gt.BuildExtensionTestSpecsFromGoTests(tests.Tests)
	if err != nil {
		panic(fmt.Sprintf("couldn't build extension test specs from registered tests: %+v", err.Error()))
	}

	ext.AddSpecs(specs)
	registry.Register(ext)

	root := &cobra.Command{
		Long: "OpenShift Tests Extension Go Test Example",
	}

	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)

	if err := func() error {
		return root.Execute()
	}(); err != nil {
		os.Exit(1)
	}
}
