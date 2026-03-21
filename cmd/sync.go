package cmd

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/smallest-inc/velocity-cli/internal/ui"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync <local-path> <instance>:<remote-path>",
	Short: "Sync files to/from an instance using rsync",
	Long: `Sync files between your local machine and a cloud instance using rsync.

Examples:
  vctl sync ./src prod-01:/home/ubuntu/src
  vctl sync prod-01:/home/ubuntu/logs ./logs`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireProject(); err != nil {
			return err
		}

		user, _ := cmd.Flags().GetString("user")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		delete, _ := cmd.Flags().GetBool("delete")

		src := args[0]
		dst := args[1]

		// Determine which argument contains the instance reference
		var instanceName string
		var localPath, remotePath string
		var toRemote bool

		if parts := strings.SplitN(dst, ":", 2); len(parts) == 2 {
			// sync local -> remote
			instanceName = parts[0]
			localPath = src
			remotePath = parts[1]
			toRemote = true
		} else if parts := strings.SplitN(src, ":", 2); len(parts) == 2 {
			// sync remote -> local
			instanceName = parts[0]
			remotePath = parts[1]
			localPath = dst
			toRemote = false
		} else {
			return fmt.Errorf("one argument must be in the form <instance>:<path>")
		}

		// Resolve instance
		stop := ui.Spinner("Resolving instance")
		inst, err := findInstance(instanceName)
		stop()
		if err != nil {
			return err
		}

		addr := inst.effectiveAddress()
		if addr == "" {
			return fmt.Errorf("instance %q has no public address", inst.Name)
		}

		// Find matching SSH key
		var keys []sshKey
		err = apiClient.Get(fmt.Sprintf("/projects/%s/cloud/ssh-keys", cfg.ProjectID), &keys)
		if err != nil {
			return fmt.Errorf("failed to fetch project SSH keys: %w", err)
		}

		// Build rsync command
		rsyncArgs := []string{"rsync", "-avz", "--progress"}

		if dryRun {
			rsyncArgs = append(rsyncArgs, "--dry-run")
		}
		if delete {
			rsyncArgs = append(rsyncArgs, "--delete")
		}

		// SSH options
		sshOpt := "ssh -o StrictHostKeyChecking=no"
		keyPath, err := findMatchingLocalKey(keys)
		if err != nil {
			ui.Warn(fmt.Sprintf("Could not find matching SSH key: %v", err))
		} else {
			sshOpt += " -i " + keyPath
		}
		rsyncArgs = append(rsyncArgs, "-e", sshOpt)

		// Build source and destination
		remoteSpec := fmt.Sprintf("%s@%s:%s", user, addr, remotePath)
		if toRemote {
			rsyncArgs = append(rsyncArgs, localPath, remoteSpec)
		} else {
			rsyncArgs = append(rsyncArgs, remoteSpec, localPath)
		}

		// Find rsync binary
		rsyncBin, err := findBinary("rsync")
		if err != nil {
			return fmt.Errorf("rsync not found in PATH: %w", err)
		}

		direction := "to"
		if !toRemote {
			direction = "from"
		}
		ui.Info(fmt.Sprintf("Syncing %s %s (%s)", direction, inst.Name, addr))

		return syscall.Exec(rsyncBin, rsyncArgs, os.Environ())
	},
}

func init() {
	syncCmd.Flags().StringP("user", "u", "ubuntu", "SSH user")
	syncCmd.Flags().Bool("dry-run", false, "Show what would be transferred")
	syncCmd.Flags().Bool("delete", false, "Delete files on destination that don't exist on source")
	rootCmd.AddCommand(syncCmd)
}
