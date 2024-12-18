package framework

import (
	"encoding/json"
	"os/exec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
)

var _ = Describe("[sig-testing] example-tests list", Label("framework"), func() {
	It("should list all specs", func() {
		specs := runList()
		Expect(len(specs)).Should(BeNumerically(">", 5))
	})

	It("should populate fields", func() {
		specs := runList()
		Expect(specs[0]).To(HaveField("Lifecycle", Not(BeEmpty())))
		Expect(specs[0]).To(HaveField("Source", "openshift:payload:example-tests"))
	})

	It("should filter specs by suite", func() {
		specs := runList("--suite", "example/fast")
		for _, spec := range specs {
			Expect(spec.Labels).ToNot(HaveKey("SLOW"))
		}
	})

	It("should filter specs by environment flags", func() {
		specs := runList("--platform", "aws")
		for _, spec := range specs {
			if !spec.EnvironmentSelector.IsEmpty() {
				Expect(spec.EnvironmentSelector).To(Equal(e.EnvironmentSelector{
					Include: `platform=="aws"`,
				}))
			}
		}
	})

})

func runList(args ...string) e.ExtensionTestSpecs {
	var result e.ExtensionTestSpecs
	args = append([]string{"list"}, args...)
	cmd := exec.Command(binary, args...)
	output, err := cmd.Output()
	Expect(err).ShouldNot(HaveOccurred(), "Expected `example-tests list` to run successfully")
	// Unmarshal the JSON output
	err = json.Unmarshal(output, &result)
	Expect(err).ShouldNot(HaveOccurred(), "Expected JSON output to unmarshal into Extension struct")
	return result
}
