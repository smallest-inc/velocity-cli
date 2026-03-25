package cmd

import (
	"fmt"
	"os"

	"github.com/smallest-inc/velocity-cli/internal/api"
	"github.com/smallest-inc/velocity-cli/internal/config"
	"github.com/smallest-inc/velocity-cli/internal/update"
	"github.com/smallest-inc/velocity-cli/internal/ui"
	"github.com/spf13/cobra"
)

var (
	// Global state loaded in PersistentPreRunE
	cfg       *config.Config
	creds     *config.Credentials
	apiClient *api.Client

	// Flag values
	flagEndpoint string
	flagToken    string
	flagProject  string
	Quiet        bool
	Verbose      bool

	version = "0.1.0"
)

var rootCmd = &cobra.Command{
	Use:   "vctl",
	Short: "Velocity CLI — manage cloud instances via the Toggle API",
	Long:  "vctl is a command-line tool for provisioning and managing cloud instances through the Toggle platform.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Compute Verbose from Quiet flag
		Verbose = !Quiet

		// Skip loading config for commands that don't need it
		if cmd.Name() == "version" {
			return nil
		}

		var err error
		cfg, err = config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		creds, err = config.LoadCredentials()
		if err != nil {
			return fmt.Errorf("failed to load credentials: %w", err)
		}

		// Env var overrides
		if v := os.Getenv("VCTL_ENDPOINT"); v != "" {
			cfg.Endpoint = v
		}
		if v := os.Getenv("VCTL_TOKEN"); v != "" {
			creds.Token = v
		}
		if v := os.Getenv("VCTL_PROJECT"); v != "" {
			cfg.ProjectID = v
		}

		// Flag overrides (highest priority)
		if flagEndpoint != "" {
			cfg.Endpoint = flagEndpoint
		}
		if flagToken != "" {
			creds.Token = flagToken
		}
		if flagProject != "" {
			cfg.ProjectID = flagProject
		}

		apiClient = api.NewClient(cfg.Endpoint, creds.Token)
		return nil
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		// Skip for upgrade/version/completion
		switch cmd.Name() {
		case "upgrade", "version", "completion":
			return
		}

		// Show cached update notification (local file read, zero latency)
		if msg := update.Notify(version); msg != "" {
			fmt.Println()
			ui.Info(msg)
		}

		// Kick off background check for next time (non-blocking)
		update.CheckInBackground(version)
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of vctl",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("vctl %s\n", version)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagEndpoint, "endpoint", "", "Toggle API endpoint (env: VCTL_ENDPOINT)")
	rootCmd.PersistentFlags().StringVar(&flagToken, "token", "", "PAT token (env: VCTL_TOKEN)")
	rootCmd.PersistentFlags().StringVar(&flagProject, "project", "", "Project ID (env: VCTL_PROJECT)")
	rootCmd.PersistentFlags().BoolVarP(&Quiet, "quiet", "q", false, "Suppress detailed output")
	rootCmd.AddCommand(versionCmd)
}

// requireAuth checks that a token is configured.
func requireAuth() error {
	if creds.Token == "" {
		return fmt.Errorf("not authenticated. Run 'vctl auth login --token <pat>' first")
	}
	return nil
}

// requireProject checks that an active project is configured.
func requireProject() error {
	if err := requireAuth(); err != nil {
		return err
	}
	if cfg.ProjectID == "" {
		return fmt.Errorf("no active project. Run 'vctl project use <handle-or-id>' first")
	}
	return nil
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
