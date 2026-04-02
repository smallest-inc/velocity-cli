package cmd

import (
	"fmt"
	"strings"

	"github.com/smallest-inc/velocity-cli/internal/config"
	"github.com/smallest-inc/velocity-cli/internal/ui"
	"github.com/spf13/cobra"
)

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "View and manage CLI settings",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := config.Load()
		if err != nil {
			return err
		}

		autoUpgrade := "on (default)"
		if c.AutoUpgrade != nil {
			if *c.AutoUpgrade {
				autoUpgrade = "on"
			} else {
				autoUpgrade = "off"
			}
		}

		fmt.Printf("  auto-upgrade:  %s\n", autoUpgrade)
		return nil
	},
}

var settingsSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a CLI setting",
	Long: `Set a CLI setting. Available settings:

  auto-upgrade  on|off   Auto-upgrade vctl after each command (default: on)`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		value := strings.ToLower(args[1])

		c, err := config.Load()
		if err != nil {
			return err
		}

		switch key {
		case "auto-upgrade":
			switch value {
			case "on", "true", "1":
				v := true
				c.AutoUpgrade = &v
			case "off", "false", "0":
				v := false
				c.AutoUpgrade = &v
			default:
				return fmt.Errorf("invalid value %q for auto-upgrade (use on/off)", value)
			}
		default:
			return fmt.Errorf("unknown setting %q (available: auto-upgrade)", key)
		}

		if err := config.Save(c); err != nil {
			return fmt.Errorf("failed to save settings: %w", err)
		}

		ui.Success(fmt.Sprintf("%s = %s", key, value))
		return nil
	},
}

func init() {
	settingsCmd.AddCommand(settingsSetCmd)
	rootCmd.AddCommand(settingsCmd)
}
