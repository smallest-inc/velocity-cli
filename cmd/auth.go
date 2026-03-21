package cmd

import (
	"fmt"

	"github.com/smallest-inc/velocity-cli/internal/config"
	"github.com/smallest-inc/velocity-cli/internal/ui"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authentication commands",
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with a personal access token",
	RunE: func(cmd *cobra.Command, args []string) error {
		token, _ := cmd.Flags().GetString("token")
		if token == "" {
			token = ui.Prompt("Enter your PAT token")
		}
		if token == "" {
			return fmt.Errorf("token is required")
		}

		// Validate token by calling the projects endpoint
		stop := ui.Spinner("Validating token")
		creds.Token = token
		apiClient.Token = token

		var projects []struct {
			ID          string `json:"id"`
			Handle      string `json:"handle"`
			DisplayName string `json:"display_name"`
		}
		err := apiClient.Get("/projects", &projects)
		stop()

		if err != nil {
			return fmt.Errorf("token validation failed: %w", err)
		}

		// Save credentials
		if err := config.SaveCredentials(&config.Credentials{Token: token}); err != nil {
			return fmt.Errorf("failed to save credentials: %w", err)
		}

		ui.Success("Authenticated successfully")
		if len(projects) > 0 {
			ui.Info(fmt.Sprintf("Found %d project(s). Use 'vctl project list' to see them.", len(projects)))

			// Auto-set project if only one exists and none is configured
			if len(projects) == 1 && cfg.ProjectID == "" {
				cfg.ProjectID = projects[0].ID
				config.Save(cfg)
				ui.Info(fmt.Sprintf("Auto-selected project: %s (%s)", projects[0].DisplayName, projects[0].Handle))
			}
		}

		return nil
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current authentication status",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Endpoint:  %s\n", ui.Cyan(cfg.Endpoint))

		if creds.Token == "" {
			fmt.Printf("Token:     %s\n", ui.Red("not set"))
		} else {
			prefix := creds.Token
			if len(prefix) > 12 {
				prefix = prefix[:12] + "..."
			}
			fmt.Printf("Token:     %s\n", ui.Green(prefix))
		}

		if cfg.ProjectID == "" {
			fmt.Printf("Project:   %s\n", ui.Yellow("not set"))
		} else {
			fmt.Printf("Project:   %s\n", ui.Green(cfg.ProjectID))
		}

		return nil
	},
}

func init() {
	authLoginCmd.Flags().String("token", "", "Personal access token")
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authStatusCmd)
	rootCmd.AddCommand(authCmd)
}
