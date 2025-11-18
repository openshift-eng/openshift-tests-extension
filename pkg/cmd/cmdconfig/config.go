package cmdconfig

import (
	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/extension"
)

func NewConfigCommand(registry *extension.Registry) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "config",
		Short:        "Manage extension configurations",
		SilenceUsage: true,
	}

	cmd.AddCommand(
		NewApplyCommand(registry),
		NewRemoveCommand(registry),
	)

	return cmd
}

