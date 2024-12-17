package framework

import (
	"encoding/json"
	"os/exec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift-eng/openshift-tests-extension/pkg/flags"
)

var _ = Describe("[sig-testing] example-tests env-flags", Label("framework"), func() {
	var result []flags.FlagVersion

	BeforeEach(func() {
		cmd := exec.Command(binary, "env-flags")
		output, err := cmd.Output()
		Expect(err).ShouldNot(HaveOccurred(), "Expected `example-tests env-flags` to run successfully")
		// Unmarshal the JSON output
		err = json.Unmarshal(output, &result)
		Expect(err).ShouldNot(HaveOccurred(), "Expected JSON output to unmarshal into Extension struct")
	})

	It("should list current flags", func() {
		Expect(result).To(Equal(flags.EnvironmentFlagsForVersion))
	})
})
