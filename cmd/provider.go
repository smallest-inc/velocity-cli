package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/smallest-inc/velocity-cli/internal/ui"
	"github.com/spf13/cobra"
)

type cloudProvider struct {
	ID            string  `json:"id"`
	Provider      string  `json:"provider"`
	RoleARN       string  `json:"role_arn"`
	ExternalID    string  `json:"external_id"`
	DefaultRegion string  `json:"default_region"`
	Verified      bool    `json:"verified"`
	VerifiedAt    *string `json:"verified_at"`
}

var providerCmd = &cobra.Command{
	Use:   "provider",
	Short: "Manage cloud providers",
}

var providerStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cloud provider configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		var providers []cloudProvider
		stop := ui.Spinner("Fetching provider configuration")
		err := apiClient.Get("/cloud/providers", &providers)
		stop()

		if err != nil {
			return err
		}

		if len(providers) == 0 {
			ui.Warn("No cloud provider configured. Run 'vctl provider setup' to configure one.")
			return nil
		}

		for _, p := range providers {
			fmt.Printf("  Provider:       %s\n", ui.Bold(p.Provider))
			fmt.Printf("  Region:         %s\n", p.DefaultRegion)
			fmt.Printf("  External ID:    %s\n", ui.Cyan(p.ExternalID))
			if p.RoleARN != "" {
				fmt.Printf("  Role ARN:       %s\n", p.RoleARN)
			} else {
				fmt.Printf("  Role ARN:       %s\n", ui.Yellow("not set"))
			}
			if p.Verified {
				fmt.Printf("  Verified:       %s\n", ui.Green("yes"))
				if p.VerifiedAt != nil {
					fmt.Printf("  Verified At:    %s\n", *p.VerifiedAt)
				}
			} else {
				fmt.Printf("  Verified:       %s\n", ui.Red("no"))
			}
			fmt.Println()
		}

		return nil
	},
}

var providerSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive cloud provider setup",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}

		// Check if provider already exists
		var existing []cloudProvider
		stop := ui.Spinner("Checking existing configuration")
		err := apiClient.Get("/cloud/providers", &existing)
		stop()

		if err != nil {
			return err
		}

		var provider cloudProvider

		if len(existing) > 0 {
			provider = existing[0]
			ui.Info(fmt.Sprintf("Existing provider found (External ID: %s)", provider.ExternalID))

			if provider.Verified {
				ui.Success("Provider is already verified.")
				return nil
			}
		} else {
			// Create new provider
			region := ui.Prompt("Default AWS region (e.g. us-east-1, ap-south-1)")
			if region == "" {
				return fmt.Errorf("region is required")
			}

			reqBody := map[string]string{
				"provider":       "aws",
				"default_region": region,
			}

			stop = ui.Spinner("Creating cloud provider configuration")
			err = apiClient.Post("/cloud/providers", reqBody, &provider)
			stop()

			if err != nil {
				return err
			}

			ui.Success("Cloud provider configuration created")
		}

		// Show IAM policy
		fmt.Println()
		ui.Info("Now set up the IAM role in your AWS account:")
		fmt.Println()

		stop = ui.Spinner("Fetching IAM policy")
		var iamPolicy map[string]interface{}
		err = apiClient.Get("/cloud/setup/iam-policy", &iamPolicy)
		stop()

		if err != nil {
			ui.Warn(fmt.Sprintf("Could not fetch IAM policy: %v", err))
		} else {
			externalID, _ := iamPolicy["external_id"].(string)
			toggleAccountID, _ := iamPolicy["toggle_account_id"].(string)
			roleName, _ := iamPolicy["role_name"].(string)

			fmt.Printf("  1. Create an IAM role named %s in your AWS account\n", ui.Bold(roleName))
			fmt.Printf("  2. Set trust policy to allow account %s with external ID:\n", ui.Cyan(toggleAccountID))
			fmt.Printf("     External ID: %s\n", ui.Bold(externalID))
			fmt.Println()
			fmt.Println("  3. Attach the following permissions policy:")

			if permsPolicy, ok := iamPolicy["permissions_policy"]; ok {
				policyJSON, _ := json.MarshalIndent(permsPolicy, "     ", "  ")
				fmt.Println(ui.Gray(string(policyJSON)))
			}
			fmt.Println()
		}

		// Prompt for ARN
		arn := ui.Prompt("Enter the IAM Role ARN")
		if arn == "" {
			ui.Info("Setup paused. Run 'vctl provider setup' again when the role is ready.")
			return nil
		}

		// Update provider with ARN
		updateBody := map[string]string{
			"role_arn": arn,
		}

		stop = ui.Spinner("Updating provider configuration")
		err = apiClient.Put(fmt.Sprintf("/cloud/providers/%s", provider.ID), updateBody, &provider)
		stop()

		if err != nil {
			return err
		}

		// Verify
		stop = ui.Spinner("Verifying cloud provider access")
		var verifyResult struct {
			Verified   bool   `json:"verified"`
			VerifiedAt string `json:"verified_at"`
			Message    string `json:"message"`
		}
		err = apiClient.Post(fmt.Sprintf("/cloud/providers/%s/verify", provider.ID), nil, &verifyResult)
		stop()

		if err != nil {
			return fmt.Errorf("verification failed: %w", err)
		}

		if verifyResult.Verified {
			ui.Success("Cloud provider verified successfully!")
			ui.Info("You can now provision instances with 'vctl instance provision'")
		} else {
			ui.Error("Verification failed. Check your IAM role configuration and try again.")
		}

		return nil
	},
}

func init() {
	providerCmd.AddCommand(providerStatusCmd)
	providerCmd.AddCommand(providerSetupCmd)
	rootCmd.AddCommand(providerCmd)
}
