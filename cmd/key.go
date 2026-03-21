package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/smallest-inc/velocity-cli/internal/ui"
	"github.com/spf13/cobra"
)

var keyCmd = &cobra.Command{
	Use:   "key",
	Short: "Manage SSH keys",
}

var keyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List SSH keys in the active project",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireProject(); err != nil {
			return err
		}

		var keys []sshKey
		stop := ui.Spinner("Fetching SSH keys")
		err := apiClient.Get(fmt.Sprintf("/projects/%s/cloud/ssh-keys", cfg.ProjectID), &keys)
		stop()

		if err != nil {
			return err
		}

		if len(keys) == 0 {
			ui.Warn("No SSH keys found. Add one with 'vctl key add <name> <path>'")
			return nil
		}

		headers := []string{"NAME", "FINGERPRINT", "CREATED"}
		var rows [][]string
		for _, k := range keys {
			created := k.CreatedAt
			if len(created) > 10 {
				created = created[:10]
			}
			rows = append(rows, []string{k.Name, k.Fingerprint, created})
		}
		ui.Table(headers, rows)
		return nil
	},
}

var keyAddCmd = &cobra.Command{
	Use:   "add <name> <public-key-path>",
	Short: "Add an SSH public key",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireProject(); err != nil {
			return err
		}

		name := args[0]
		keyPath := args[1]

		// Expand ~ if present
		if strings.HasPrefix(keyPath, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			keyPath = home + keyPath[1:]
		}

		data, err := os.ReadFile(keyPath)
		if err != nil {
			return fmt.Errorf("failed to read public key file: %w", err)
		}

		pubKey := strings.TrimSpace(string(data))
		fields := strings.Fields(pubKey)
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "ssh-") {
			return fmt.Errorf("file does not appear to be a valid SSH public key")
		}

		reqBody := map[string]string{
			"name":       name,
			"public_key": pubKey,
		}

		stop := ui.Spinner("Adding SSH key")
		var result sshKey
		err = apiClient.Post(fmt.Sprintf("/projects/%s/cloud/ssh-keys", cfg.ProjectID), reqBody, &result)
		stop()

		if err != nil {
			return err
		}

		ui.Success(fmt.Sprintf("SSH key %q added (fingerprint: %s)", result.Name, result.Fingerprint))
		return nil
	},
}

var keyRemoveCmd = &cobra.Command{
	Use:   "remove <name-or-id>",
	Short: "Remove an SSH key",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireProject(); err != nil {
			return err
		}

		target := args[0]

		// Fetch all keys to resolve by name
		var keys []sshKey
		stop := ui.Spinner("Fetching SSH keys")
		err := apiClient.Get(fmt.Sprintf("/projects/%s/cloud/ssh-keys", cfg.ProjectID), &keys)
		stop()

		if err != nil {
			return err
		}

		var found *sshKey
		for i := range keys {
			if keys[i].Name == target || keys[i].ID == target {
				found = &keys[i]
				break
			}
		}

		if found == nil {
			return fmt.Errorf("SSH key %q not found", target)
		}

		stop = ui.Spinner("Removing SSH key")
		var result map[string]string
		err = apiClient.Delete(fmt.Sprintf("/projects/%s/cloud/ssh-keys/%s", cfg.ProjectID, found.ID), &result)
		stop()

		if err != nil {
			return err
		}

		ui.Success(fmt.Sprintf("SSH key %q removed", found.Name))
		return nil
	},
}

func init() {
	keyCmd.AddCommand(keyListCmd)
	keyCmd.AddCommand(keyAddCmd)
	keyCmd.AddCommand(keyRemoveCmd)
	rootCmd.AddCommand(keyCmd)
}
