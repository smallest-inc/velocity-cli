package cmd

import (
	"fmt"

	"github.com/smallest-inc/velocity-cli/internal/config"
	"github.com/smallest-inc/velocity-cli/internal/ui"
	"github.com/spf13/cobra"
)

type project struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	RootUserID  string `json:"root_user_id"`
}

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage projects",
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		var projects []project
		stop := ui.Spinner("Fetching projects")
		err := apiClient.Get("/projects", &projects)
		stop()

		if err != nil {
			return err
		}

		if len(projects) == 0 {
			ui.Warn("No projects found.")
			return nil
		}

		headers := []string{"ID", "HANDLE", "DISPLAY NAME", "ACTIVE"}
		var rows [][]string
		for _, p := range projects {
			active := ""
			if p.ID == cfg.ProjectID {
				active = ui.Green("*")
			}
			rows = append(rows, []string{p.ID, p.Handle, p.DisplayName, active})
		}
		ui.Table(headers, rows)
		return nil
	},
}

var projectUseCmd = &cobra.Command{
	Use:   "use <handle-or-id>",
	Short: "Set the active project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		target := args[0]

		var projects []project
		stop := ui.Spinner("Fetching projects")
		err := apiClient.Get("/projects", &projects)
		stop()

		if err != nil {
			return err
		}

		var found *project
		for i := range projects {
			if projects[i].Handle == target || projects[i].ID == target {
				found = &projects[i]
				break
			}
		}

		if found == nil {
			return fmt.Errorf("project %q not found", target)
		}

		cfg.ProjectID = found.ID
		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		ui.Success(fmt.Sprintf("Active project set to: %s (%s)", found.DisplayName, found.Handle))
		return nil
	},
}

func init() {
	projectCmd.AddCommand(projectListCmd)
	projectCmd.AddCommand(projectUseCmd)
	rootCmd.AddCommand(projectCmd)
}
