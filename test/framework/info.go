package framework

import (
	"encoding/json"
	"os/exec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift-eng/openshift-tests-extension/pkg/extension"
)

var _ = Describe("[sig-testing] example-tests info", Label("framework"), func() {
	var result extension.Extension

	BeforeEach(func() {
		cmd := exec.Command(binary, "info")
		output, err := cmd.Output()
		Expect(err).ShouldNot(HaveOccurred(), "Expected `example-tests info` to run successfully")
		// Unmarshal the JSON output
		err = json.Unmarshal(output, &result)
		Expect(err).ShouldNot(HaveOccurred(), "Expected JSON output to unmarshal into Extension struct")
	})

	It("should have build metadata", func() {
		Expect(result.Source.Commit).To(Not(BeEmpty()))
		Expect(result.Source.BuildDate).To(Not(BeEmpty()))
		Expect(result.Source.GitTreeState).To(Not(BeEmpty()))
	})

	It("should have the expected suites", func() {
		Expect(result.Suites).To(HaveLen(3), "expected 3 suites")
		Expect(result.Suites).To(ContainElement(HaveField("Name", Equal("example/tests"))), "Expected to contain a suite with name 'example/tests'")
		Expect(result.Suites).To(ContainElement(HaveField("Name", Equal("example/fast"))),
			"Expected to contain a suite with name 'example/fast'")
	})

	It("should have the correct component information", func() {
		Expect(result.Component.Product).To(Equal("openshift"), "Expected product to be 'openshift'")
		Expect(result.Component.Kind).To(Equal("payload"), "Expected type to be 'payload'")
		Expect(result.Component.Name).To(Equal("example-tests"), "Expected name to be 'default'")
	})

	It("should be able to log information", func() {
		GinkgoLogr.Info("creating resource group", "resourceGroup", "resourceGroupName")
	})
})
