package cmdenvflags

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/openshift-eng/openshift-tests-extension/pkg/flags"
	"github.com/spf13/cobra"
)

func NewEnvironmentFlagListCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "env-flags",
		Short:        "List all environment flags for the version",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			envFlags, err := json.MarshalIndent(flags.EnvironmentFlagsForVersion, "", "    ")
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "%s\n", string(envFlags))
			return nil
		},
	}
}
