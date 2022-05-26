package cmd

import (
	"fmt"
	"github.com/loft-sh/devspace/pkg/devspace/config"
	"github.com/loft-sh/devspace/pkg/devspace/config/loader"
	"github.com/loft-sh/devspace/pkg/devspace/dependency"
	"github.com/loft-sh/devspace/pkg/devspace/dependency/types"
	"github.com/loft-sh/devspace/pkg/devspace/hook"
	"github.com/loft-sh/devspace/pkg/devspace/plugin"
	"io"
	"os"

	"github.com/loft-sh/devspace/cmd/flags"
	"github.com/loft-sh/devspace/pkg/util/factory"
	logger "github.com/loft-sh/devspace/pkg/util/log"
	"github.com/loft-sh/devspace/pkg/util/message"
	"github.com/pkg/errors"
	yaml "gopkg.in/yaml.v2"

	"github.com/spf13/cobra"
)

// PrintCmd is a struct that defines a command call for "print"
type PrintCmd struct {
	*flags.GlobalFlags

	Out       io.Writer
	SkipInfo  bool
	EagerVars bool

	Dependency string
}

// NewPrintCmd creates a new devspace print command
func NewPrintCmd(f factory.Factory, globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &PrintCmd{
		GlobalFlags: globalFlags,
		Out:         os.Stdout,
	}

	printCmd := &cobra.Command{
		Use:   "print",
		Short: "Print displays the configuration",
		Long: `
#######################################################
################## devspace print #####################
#######################################################
Prints the configuration for the current or given 
profile after all patching and variable substitution
#######################################################`,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			plugin.SetPluginCommand(cobraCmd, args)
			return cmd.Run(f)
		},
	}

	printCmd.Flags().BoolVar(&cmd.SkipInfo, "skip-info", false, "When enabled, only prints the configuration without additional information")
	printCmd.Flags().StringVar(&cmd.Dependency, "dependency", "", "The dependency to print the config from. Use dot to access nested dependencies (e.g. dep1.dep2)")
	printCmd.Flags().BoolVar(&cmd.EagerVars, "eager-vars", false, "When enabled, eagerly fill variables")

	return printCmd
}

// Run executes the command logic
func (cmd *PrintCmd) Run(f factory.Factory) error {
	// Set config root
	log := f.GetLog()
	configOptions := cmd.ToConfigOptions(log)
	configLoader := f.NewConfigLoader(cmd.ConfigPath)
	configExists, err := configLoader.SetDevSpaceRoot(log)
	if err != nil {
		return err
	} else if !configExists {
		return errors.New(message.ConfigNotFound)
	}

	// create kubectl client
	client, err := f.NewKubeClientFromContext(cmd.KubeContext, cmd.Namespace, cmd.SwitchContext)
	if err != nil {
		log.Warnf("Unable to create new kubectl client: %v", err)
	}
	configOptions.KubeClient = client

	// load config
	var loadedConfig config.Config
	if cmd.EagerVars {
		loadedConfig, err = configLoader.LoadWithParser(loader.NewEagerParser(), configOptions, log)
		if err != nil {
			return err
		}
	} else {
		loadedConfig, err = configLoader.Load(configOptions, log)
		if err != nil {
			return err
		}
	}

	// resolve dependencies
	dependencies, err := dependency.NewManager(loadedConfig, client, configOptions, log).ResolveAll(dependency.ResolveOptions{
		Silent: true,
	})
	if err != nil {
		log.Warnf("Error resolving dependencies: %v", err)
	}

	// Execute plugin hook
	err = hook.ExecuteHooks(client, loadedConfig, dependencies, nil, log, "print")
	if err != nil {
		return err
	}

	if cmd.Dependency != "" {
		dep := dependency.GetDependencyByPath(dependencies, cmd.Dependency)
		if dep == nil {
			return fmt.Errorf("couldn't find dependency %s: make sure it gets loaded correctly", cmd.Dependency)
		}

		loadedConfig = dep.Config()
	}

	bsConfig, err := yaml.Marshal(loadedConfig.Config())
	if err != nil {
		return err
	}

	if !cmd.SkipInfo {
		err = printExtraInfo(loadedConfig, dependencies, log)
		if err != nil {
			return err
		}
	}

	if cmd.Out != nil {
		_, err := cmd.Out.Write(bsConfig)
		if err != nil {
			return err
		}
	} else {
		log.WriteString(string(bsConfig))
	}

	return nil
}

func printExtraInfo(config config.Config, dependencies []types.Dependency, log logger.Logger) error {
	log.WriteString("\n-------------------\n\nVars:\n")

	headerColumnNames := []string{"Name", "Value"}
	values := [][]string{}
	resolvedVars := config.Variables()
	for varName, varValue := range resolvedVars {
		values = append(values, []string{
			varName,
			fmt.Sprintf("%v", varValue),
		})
	}

	if len(values) > 0 {
		logger.PrintTable(log, headerColumnNames, values)
	} else {
		log.Info("No vars found")
	}

	log.WriteString("\n-------------------\n\nLoaded path: " + config.Path() + "\n\n-------------------\n\n")

	if len(dependencies) > 0 {
		log.WriteString("Dependency Tree:\n\n> Root\n")
		for _, dep := range dependencies {
			printDependencyRecursive("--", dep, log)
		}
		log.WriteString("\n-------------------\n\n")
	}

	return nil
}

func printDependencyRecursive(prefix string, dep types.Dependency, log logger.Logger) {
	log.WriteString(prefix + "> " + dep.Name() + " (ID: " + dep.ID()[:5] + ")\n")
	for _, child := range dep.Children() {
		printDependencyRecursive(prefix+"--", child, log)
	}
}
