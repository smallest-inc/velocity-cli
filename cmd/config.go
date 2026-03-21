package cmd

import (
	"fmt"

	"github.com/smallest-inc/velocity-cli/internal/ui"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage launch configurations",
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available launch configurations",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireProject(); err != nil {
			return err
		}

		var configs []launchConfig
		stop := ui.Spinner("Fetching launch configurations")
		err := apiClient.Get(fmt.Sprintf("/projects/%s/cloud/launch-configs", cfg.ProjectID), &configs)
		stop()

		if err != nil {
			return err
		}

		if len(configs) == 0 {
			ui.Warn("No launch configurations found.")
			return nil
		}

		headers := []string{"NAME", "TYPE", "REGION", "TEMPLATE", "SCOPE", "DEFAULT"}
		var rows [][]string
		for _, c := range configs {
			scope := "tenant"
			if c.ProjectID != nil {
				scope = "project"
			}
			isDefault := ""
			if c.IsDefault {
				isDefault = ui.Green("*")
			}
			templateInfo := c.LaunchTemplateID
			if c.LaunchTemplateVersion != "" {
				templateInfo += " v" + c.LaunchTemplateVersion
			}
			rows = append(rows, []string{
				c.Name,
				c.InstanceType,
				c.Region,
				templateInfo,
				scope,
				isDefault,
			})
		}
		ui.Table(headers, rows)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configListCmd)
	rootCmd.AddCommand(configCmd)
}
