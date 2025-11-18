package cmdrun

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	"github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/flags"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"
)

func NewRunSuiteCommand(registry *extension.Registry) *cobra.Command {
	opts := struct {
		componentFlags   *flags.ComponentFlags
		outputFlags      *flags.OutputFlags
		concurrencyFlags *flags.ConcurrencyFlags
		envFlags         *flags.EnvironmentalFlags
		junitPath        string
	}{
		componentFlags:   flags.NewComponentFlags(),
		outputFlags:      flags.NewOutputFlags(),
		concurrencyFlags: flags.NewConcurrencyFlags(),
		envFlags:         flags.NewEnvironmentalFlags(),
		junitPath:        "",
	}

	cmd := &cobra.Command{
		Use: "run-suite NAME",
		Short: "Run a group of tests by suite. This is more limited than origin, and intended for light local " +
			"development use. Orchestration parameters, scheduling, isolation, etc are not obeyed, and Ginkgo tests are executed serially.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancelCause := context.WithCancelCause(context.Background())
			defer cancelCause(errors.New("exiting"))

			abortCh := make(chan os.Signal, 2)
			go func() {
				<-abortCh
				fmt.Fprintf(os.Stderr, "Interrupted, terminating tests")
				cancelCause(errors.New("interrupt received"))

				select {
				case sig := <-abortCh:
					fmt.Fprintf(os.Stderr, "Interrupted twice, exiting (%s)", sig)
					switch sig {
					case syscall.SIGINT:
						os.Exit(130)
					default:
						os.Exit(130) // if we were interrupted, never return zero.
					}

				case <-time.After(30 * time.Minute): // allow time for cleanup.  If we finish before this, we'll exit
					fmt.Fprintf(os.Stderr, "Timed out during cleanup, exiting")
					os.Exit(130) // if we were interrupted, never return zero.
				}
			}()
			signal.Notify(abortCh, syscall.SIGINT, syscall.SIGTERM)

			ext := registry.Get(opts.componentFlags.Component)
			if ext == nil {
				return fmt.Errorf("component not found: %s", opts.componentFlags.Component)
			}
			if len(args) != 1 {
				return fmt.Errorf("must specify one suite name")
			}
			suite, err := ext.GetSuite(args[0])
			if err != nil {
				return errors.Wrapf(err, "couldn't find suite: %s", args[0])
			}

			compositeWriter := extensiontests.NewCompositeResultWriter()
			defer func() {
				if err = compositeWriter.Flush(); err != nil {
					fmt.Fprintf(os.Stderr, "failed to write results: %v\n", err)
				}
			}()

			// JUnit writer if needed
			if opts.junitPath != "" {
				junitWriter, err := extensiontests.NewJUnitResultWriter(opts.junitPath, suite.Name)
				if err != nil {
					return errors.Wrap(err, "couldn't create junit writer")
				}
				compositeWriter.AddWriter(junitWriter)
			}

			// JSON writer
			jsonWriter, err := extensiontests.NewJSONResultWriter(os.Stdout,
				extensiontests.ResultFormat(opts.outputFlags.Output))
			if err != nil {
				return err
			}
			compositeWriter.AddWriter(jsonWriter)

		specs, err := ext.GetSpecs().Filter(suite.Qualifiers)
		if err != nil {
			return errors.Wrap(err, "couldn't filter specs")
		}

		// Group specs by required configs
		specsByConfig := groupSpecsByConfig(specs)

		// Track overall errors
		var runErrors []error

		// First, run specs that don't require any config
		if noConfigSpecs, ok := specsByConfig[""]; ok && len(noConfigSpecs) > 0 {
			fmt.Fprintf(os.Stderr, "Running %d test(s) without config requirements...\n", len(noConfigSpecs))
			if err := noConfigSpecs.Run(ctx, compositeWriter, opts.concurrencyFlags.MaxConcurency); err != nil {
				runErrors = append(runErrors, err)
			}
			delete(specsByConfig, "")
		}

		// Then run specs grouped by config
		for configName, configSpecs := range specsByConfig {
			if err := runSpecsWithConfig(
				ctx,
				ext,
				configName,
				configSpecs,
				compositeWriter,
				opts.concurrencyFlags.MaxConcurency,
				*opts.envFlags,
			); err != nil {
				runErrors = append(runErrors, err)
			}
		}

		// Return combined errors if any
		if len(runErrors) > 0 {
			return fmt.Errorf("%d test group(s) failed", len(runErrors))
		}

		return nil
		},
	}
	opts.componentFlags.BindFlags(cmd.Flags())
	opts.outputFlags.BindFlags(cmd.Flags())
	opts.concurrencyFlags.BindFlags(cmd.Flags())
	opts.envFlags.BindFlags(cmd.Flags())
	cmd.Flags().StringVarP(&opts.junitPath, "junit-path", "j", opts.junitPath, "write results to junit XML")

	return cmd
}

// extractRequiredConfigs extracts the set of config names required by the given specs
func extractRequiredConfigs(specs extensiontests.ExtensionTestSpecs) sets.Set[string] {
	configs := sets.New[string]()
	for _, spec := range specs {
		for label := range spec.Labels {
			if strings.HasPrefix(label, "Config:") {
				configName := strings.TrimPrefix(label, "Config:")
				configs.Insert(configName)
			}
		}
	}
	return configs
}

// groupSpecsByConfig groups specs by the configs they require. Specs with no config requirement
// are grouped under an empty string key.
func groupSpecsByConfig(specs extensiontests.ExtensionTestSpecs) map[string]extensiontests.ExtensionTestSpecs {
	groups := make(map[string]extensiontests.ExtensionTestSpecs)
	
	for _, spec := range specs {
		var configName string
		// Find the config requirement for this spec
		for label := range spec.Labels {
			if strings.HasPrefix(label, "Config:") {
				configName = strings.TrimPrefix(label, "Config:")
				break
			}
		}
		
		// Group by config name (empty string for no config)
		groups[configName] = append(groups[configName], spec)
	}
	
	return groups
}

// runSpecsWithConfig applies a config, runs the specs, then removes the config
func runSpecsWithConfig(
	ctx context.Context,
	ext *extension.Extension,
	configName string,
	specs extensiontests.ExtensionTestSpecs,
	writer extensiontests.ResultWriter,
	maxConcurrency int,
	envFlags flags.EnvironmentalFlags,
) error {
	// Find the config
	var config *extension.Config
	for i := range ext.Configs {
		if ext.Configs[i].Name == configName {
			config = &ext.Configs[i]
			break
		}
	}

	if config == nil {
		return fmt.Errorf("config %q not found but required by tests", configName)
	}

	// Apply the config
	if config.Apply != nil {
		fmt.Fprintf(os.Stderr, "Applying config %q for %d test(s)...\n", configName, len(specs))
		if err := config.Apply(ctx, envFlags); err != nil {
			return fmt.Errorf("failed to apply config %q: %w", configName, err)
		}
	}

	// Run the specs
	err := specs.Run(ctx, writer, maxConcurrency)

	// Remove the config
	if config.Remove != nil {
		fmt.Fprintf(os.Stderr, "Removing config %q...\n", configName)
		if removeErr := config.Remove(ctx, envFlags); removeErr != nil {
			// Log warning but don't fail the overall run
			fmt.Fprintf(os.Stderr, "Warning: failed to remove config %q: %v\n", configName, removeErr)
		}
	}

	return err
}
