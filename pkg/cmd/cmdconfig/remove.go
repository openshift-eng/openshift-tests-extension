package cmdconfig

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	"github.com/openshift-eng/openshift-tests-extension/pkg/flags"
)

func NewRemoveCommand(registry *extension.Registry) *cobra.Command {
	componentFlags := flags.NewComponentFlags()
	envFlags := flags.NewEnvironmentalFlags()
	var configName string

	cmd := &cobra.Command{
		Use:          "remove",
		Short:        "Remove a configuration",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ext := registry.Get(componentFlags.Component)
			if ext == nil {
				return fmt.Errorf("couldn't find the component %q", componentFlags.Component)
			}

			if configName == "" {
				return fmt.Errorf("--config flag is required")
			}

			// Find the config by name
			var config *extension.Config
			for i := range ext.Configs {
				if ext.Configs[i].Name == configName {
					config = &ext.Configs[i]
					break
				}
			}

			if config == nil {
				return fmt.Errorf("config %q not found", configName)
			}

			if config.Remove == nil {
				return fmt.Errorf("config %q does not have a Remove function", configName)
			}

			ctx := context.Background()
			if err := config.Remove(ctx, *envFlags); err != nil {
				return fmt.Errorf("failed to remove config %q: %w", configName, err)
			}

			fmt.Printf("Successfully removed config %q\n", configName)
			return nil
		},
	}

	componentFlags.BindFlags(cmd.Flags())
	envFlags.BindFlags(cmd.Flags())
	cmd.Flags().StringVar(&configName, "config", "", "Name of the configuration to remove")

	return cmd
}

