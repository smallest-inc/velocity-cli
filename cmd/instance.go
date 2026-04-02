package cmd

import (
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	autokeys "github.com/smallest-inc/velocity-cli/internal/keys"
	"github.com/smallest-inc/velocity-cli/internal/config"
	"github.com/smallest-inc/velocity-cli/internal/ui"
	"github.com/spf13/cobra"
)

type instance struct {
	ID               string  `json:"id"`
	ProjectID        string  `json:"project_id"`
	Name             string  `json:"name"`
	InstanceID       string  `json:"instance_id"`
	InstanceState    string  `json:"instance_state"`
	InstanceType     string  `json:"instance_type"`
	Region           string  `json:"region"`
	PublicIP         string  `json:"public_ip"`
	PrivateIP        string  `json:"private_ip"`
	ElasticIPAddress string  `json:"elastic_ip_address"`
	DomainEnabled    bool    `json:"domain_enabled"`
	DomainName       string  `json:"domain_name"`
	AvailabilityZone string  `json:"availability_zone"`
	RodentNodeID     string  `json:"rodent_node_id"`
	LaunchConfigID   string  `json:"launch_config_id"`
	ProvisionedBy    string  `json:"provisioned_by"`
	ProvisionedAt    string  `json:"provisioned_at"`
	TerminatedAt     *string `json:"terminated_at"`
}

type launchConfig struct {
	ID                    string  `json:"id"`
	Name                  string  `json:"name"`
	InstanceType          string  `json:"instance_type"`
	Region                string  `json:"region"`
	LaunchTemplateID      string  `json:"launch_template_id"`
	LaunchTemplateVersion string  `json:"launch_template_version"`
	ProjectID             *string `json:"project_id"`
	AMI                   string  `json:"ami"`
	EBSVolumeGB           int     `json:"ebs_volume_gb"`
	IsDefault             bool    `json:"is_default"`
	DefaultHostedZoneID   string  `json:"default_hosted_zone_id"`
	DomainPrefix          string  `json:"domain_prefix"`
	UseSpot               bool    `json:"use_spot"`
	SpotMaxPrice          string  `json:"spot_max_price"`
}

type sshKey struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	ProjectID   string `json:"project_id"`
	CreatedAt   string `json:"created_at"`
}

// effectiveAddress returns the best available address for an instance.
func (inst *instance) effectiveAddress() string {
	if inst.ElasticIPAddress != "" {
		return inst.ElasticIPAddress
	}
	if inst.DomainName != "" {
		return inst.DomainName
	}
	if inst.PublicIP != "" {
		return inst.PublicIP
	}
	return ""
}

// stateColor returns a color-coded state string.
func stateColor(state string) string {
	switch state {
	case "running":
		return ui.Green(state)
	case "stopped":
		return ui.Red(state)
	case "pending", "stopping":
		return ui.Yellow(state)
	case "terminated", "shutting-down":
		return ui.Gray(state)
	default:
		return state
	}
}

// findInstance finds an instance by name, Toggle ID, or AWS instance ID.
// If multiple instances match a name, returns an error asking for the ID.
func findInstance(nameOrID string) (*instance, error) {
	var instances []instance
	err := apiClient.Get(fmt.Sprintf("/projects/%s/cloud/instances", cfg.ProjectID), &instances)
	if err != nil {
		return nil, err
	}

	// Exact ID match first (always unique)
	for i := range instances {
		if instances[i].ID == nameOrID || instances[i].InstanceID == nameOrID {
			return &instances[i], nil
		}
	}

	// Name match — check for ambiguity
	var matches []*instance
	for i := range instances {
		if instances[i].Name == nameOrID {
			matches = append(matches, &instances[i])
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		msg := fmt.Sprintf("multiple instances named %q — use the instance ID instead:\n", nameOrID)
		for _, m := range matches {
			msg += fmt.Sprintf("  %s (%s, %s)\n", m.ID, m.InstanceID, m.InstanceState)
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return nil, fmt.Errorf("instance %q not found", nameOrID)
}

// pollInstanceStatus polls until the instance reaches the target state or times out.
func pollInstanceStatus(instanceID string, targetState string, timeout time.Duration) (*instance, error) {
	deadline := time.Now().Add(timeout)
	path := fmt.Sprintf("/projects/%s/cloud/instances/%s/status", cfg.ProjectID, instanceID)
	for {
		var inst instance
		if err := apiClient.Get(path, &inst); err != nil {
			return nil, err
		}
		if inst.InstanceState == targetState {
			return &inst, nil
		}
		if time.Now().After(deadline) {
			return &inst, fmt.Errorf("timed out waiting for state %q (current: %s)", targetState, inst.InstanceState)
		}
		time.Sleep(3 * time.Second)
	}
}

var instanceCmd = &cobra.Command{
	Use:   "instance",
	Short: "Manage cloud instances",
}

var instanceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all instances in the active project",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireProject(); err != nil {
			return err
		}

		var instances []instance
		stop := ui.Spinner("Fetching instances")
		err := apiClient.Get(fmt.Sprintf("/projects/%s/cloud/instances", cfg.ProjectID), &instances)
		stop()

		if err != nil {
			return err
		}

		if len(instances) == 0 {
			ui.Warn("No instances found.")
			return nil
		}

		headers := []string{"NAME", "STATE", "INSTANCE ID", "TYPE", "ADDRESS", "REGION"}
		var rows [][]string
		for _, inst := range instances {
			addr := inst.effectiveAddress()
			if inst.DomainEnabled && inst.DomainName != "" {
				addr = inst.DomainName
			}
			rows = append(rows, []string{
				inst.Name,
				stateColor(inst.InstanceState),
				inst.InstanceID,
				inst.InstanceType,
				addr,
				inst.Region,
			})
		}
		ui.Table(headers, rows)
		return nil
	},
}

var instanceProvisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Provision a new cloud instance",
	Long: `Provision a new cloud instance.

Interactive mode (default when stdin is a TTY):
  vctl instance provision

Non-interactive mode (for scripting and agentic control):
  vctl instance provision --name prod-01 --launch-config <id-or-name> --ssh-keys <name1>,<name2>
  vctl instance provision --name prod-01 --launch-config "Velocity Dev" --ssh-keys raam-ed25519 --instance-type t3.large`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireProject(); err != nil {
			return err
		}

		// Fetch launch configs (needed for both modes)
		ui.Step(Verbose, "Fetching launch configurations from Toggle")
		var configs []launchConfig
		stop := ui.Spinner("Fetching launch configurations")
		err := apiClient.Get(fmt.Sprintf("/projects/%s/cloud/launch-configs", cfg.ProjectID), &configs)
		stop()
		if err != nil {
			return err
		}
		if len(configs) == 0 {
			return fmt.Errorf("no launch configurations available. Create one first")
		}

		// Fetch SSH keys (needed for both modes)
		ui.Step(Verbose, "Fetching SSH keys from Toggle")
		var keys []sshKey
		stop = ui.Spinner("Fetching SSH keys")
		err = apiClient.Get(fmt.Sprintf("/projects/%s/cloud/ssh-keys", cfg.ProjectID), &keys)
		stop()
		if err != nil {
			return err
		}

		// Resolve parameters — flags take precedence, fall back to interactive prompts
		nameFlag, _ := cmd.Flags().GetString("name")
		lcFlag, _ := cmd.Flags().GetString("launch-config")
		keysFlag, _ := cmd.Flags().GetString("ssh-keys")
		autoKeysFlag, _ := cmd.Flags().GetBool("auto-keys")
		typeFlag, _ := cmd.Flags().GetString("instance-type")
		domainFlag, _ := cmd.Flags().GetString("domain")
		zoneFlag, _ := cmd.Flags().GetString("hosted-zone")
		noWaitFlag, _ := cmd.Flags().GetBool("no-wait")
		spotFlag, _ := cmd.Flags().GetBool("spot")
		onDemandFlag, _ := cmd.Flags().GetBool("on-demand")

		// Determine mode: if --name flag is set, treat as non-interactive (skip all prompts)
		interactive := nameFlag == "" && ui.IsInteractive()

		// --- Name ---
		name := nameFlag
		if name == "" {
			if !interactive {
				return fmt.Errorf("--name is required in non-interactive mode")
			}
			name = ui.Prompt("Instance name")
			if name == "" {
				return fmt.Errorf("instance name is required")
			}
		}

		// --- Launch Config ---
		var selectedConfig launchConfig
		if lcFlag != "" {
			found := false
			for _, c := range configs {
				if c.ID == lcFlag || c.Name == lcFlag {
					selectedConfig = c
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("launch config %q not found", lcFlag)
			}
		} else {
			if !interactive {
				// Use default if available
				for _, c := range configs {
					if c.IsDefault {
						selectedConfig = c
						break
					}
				}
				if selectedConfig.ID == "" {
					return fmt.Errorf("--launch-config is required (no default config available)")
				}
			} else {
				configNames := make([]string, len(configs))
				for i, c := range configs {
					scope := "tenant"
					if c.ProjectID != nil {
						scope = "project"
					}
					configNames[i] = fmt.Sprintf("%s (%s, %s) [%s]", c.Name, c.InstanceType, c.Region, scope)
				}
				configIdx, err := ui.Select("Select launch configuration", configNames)
				if err != nil {
					return err
				}
				selectedConfig = configs[configIdx]
			}
		}

		// --- SSH Keys ---
		// Determine if auto-key management should be used:
		// - Explicit --ssh-keys flag: use those keys, no auto-gen
		// - Explicit --auto-keys flag: force auto-gen
		// - No flag provided: auto-gen (implied)
		// - Interactive mode: offer choice
		var selectedKeyIDs []string
		useAutoKeys := false

		if keysFlag != "" {
			// Explicit key selection — resolve by name/ID
			requestedKeys := strings.Split(keysFlag, ",")
			for _, req := range requestedKeys {
				req = strings.TrimSpace(req)
				found := false
				for _, k := range keys {
					if k.ID == req || k.Name == req {
						selectedKeyIDs = append(selectedKeyIDs, k.ID)
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("SSH key %q not found", req)
				}
			}
		} else if autoKeysFlag || !interactive {
			// Non-interactive with no --ssh-keys: auto-generate
			useAutoKeys = true
		} else {
			// Interactive: offer choice
			options := []string{"Auto-manage SSH key (recommended)"}
			for _, k := range keys {
				options = append(options, fmt.Sprintf("%s (%s)", k.Name, k.Fingerprint[:16]+"..."))
			}
			idx, err := ui.Select("SSH key mode", options)
			if err != nil {
				return err
			}
			if idx == 0 {
				useAutoKeys = true
			} else {
				// They selected a specific key
				selectedKeyIDs = append(selectedKeyIDs, keys[idx-1].ID)
			}
		}

		var autoKeyPrivPath string
		var autoKeySSHID string
		var autoKeySSHName string

		if useAutoKeys {
			ui.Step(Verbose, "Generating ed25519 keypair for auto SSH key management")
			ui.Info("Generating ephemeral SSH keypair...")
			pubKey, privPath, err := autokeys.GenerateKeyPair()
			if err != nil {
				return fmt.Errorf("failed to generate SSH key: %w", err)
			}
			autoKeyPrivPath = privPath
			autoKeySSHName = filepath.Base(privPath)

			// Upload to Toggle
			var uploaded sshKey
			uploadReq := map[string]string{
				"name":       autoKeySSHName,
				"public_key": pubKey,
			}
			if err := apiClient.Post(fmt.Sprintf("/projects/%s/cloud/ssh-keys", cfg.ProjectID), uploadReq, &uploaded); err != nil {
				os.Remove(privPath)
				os.Remove(privPath + ".pub")
				return fmt.Errorf("failed to upload SSH key: %w", err)
			}

			autoKeySSHID = uploaded.ID
			selectedKeyIDs = append(selectedKeyIDs, uploaded.ID)
			ui.Success(fmt.Sprintf("SSH key generated and uploaded (private key: %s)", privPath))
		}

		// --- Build request ---
		reqBody := map[string]interface{}{
			"name":             name,
			"launch_config_id": selectedConfig.ID,
			"ssh_key_ids":      selectedKeyIDs,
		}

		// Overrides
		overrides := map[string]interface{}{}
		if typeFlag != "" {
			overrides["instance_type"] = typeFlag
		} else if interactive {
			if v := ui.Prompt(fmt.Sprintf("Instance type (enter for '%s')", selectedConfig.InstanceType)); v != "" {
				overrides["instance_type"] = v
			}
		}
		if len(overrides) > 0 {
			reqBody["overrides"] = overrides
		}

		// Spot instance override: --spot forces spot, --on-demand forces on-demand.
		// If neither specified, the launch config default is used (no override sent).
		if spotFlag {
			reqBody["use_spot"] = true
		} else if onDemandFlag {
			reqBody["use_spot"] = false
		}

		// Domain — enabled by default, use --no-domain to opt out
		noDomainFlag, _ := cmd.Flags().GetBool("no-domain")

		if !noDomainFlag {
			// Resolve hosted zone
			type hostedZone struct {
				ID          string `json:"ID"`
				Name        string `json:"Name"`
				RecordCount int64  `json:"RecordCount"`
			}
			var zones []hostedZone

			// Default zone from the selected launch config
			defaultZoneID := selectedConfig.DefaultHostedZoneID

			var providers []struct {
				ID string `json:"id"`
			}
			if err := apiClient.Get("/cloud/providers", &providers); err == nil && len(providers) > 0 {
				apiClient.Get(fmt.Sprintf("/cloud/providers/%s/dns/hosted-zones", providers[0].ID), &zones)
			}

			subdomain := domainFlag
			selectedZoneID := zoneFlag

			if len(zones) > 0 {
				if interactive {
					// Prompt for subdomain
					subdomain = ui.Prompt(fmt.Sprintf("Subdomain (enter for '%s')", name))
					if subdomain == "" {
						subdomain = name
					}

					// Prompt for zone if multiple
					if selectedZoneID == "" {
						if len(zones) == 1 {
							selectedZoneID = zones[0].ID
							ui.Info(fmt.Sprintf("Using hosted zone: %s", zones[0].Name))
						} else {
							zoneNames := make([]string, len(zones))
							for i, z := range zones {
								zoneNames[i] = fmt.Sprintf("%s (%d records)", z.Name, z.RecordCount)
							}
							zoneIdx, err := ui.Select("Select hosted zone", zoneNames)
							if err == nil {
								selectedZoneID = zones[zoneIdx].ID
							}
						}
					}
				} else {
					if subdomain == "" {
						subdomain = name
					}
					if selectedZoneID == "" && defaultZoneID != "" {
						selectedZoneID = defaultZoneID
					}
				}
			} else if defaultZoneID != "" {
				// No zones fetched but launch config has a default — use it
				ui.Step(Verbose, "Using default hosted zone from launch config")
				selectedZoneID = defaultZoneID
				if subdomain == "" {
					if interactive {
						subdomain = ui.Prompt(fmt.Sprintf("Subdomain (enter for '%s')", name))
					}
					if subdomain == "" {
						subdomain = name
					}
				}
			} else {
				ui.Step(Verbose, "No hosted zones available, skipping domain")
			}

			if selectedZoneID != "" {
				reqBody["domain"] = map[string]interface{}{
					"enabled":        true,
					"subdomain":      subdomain,
					"hosted_zone_id": selectedZoneID,
				}
				ui.Step(Verbose, fmt.Sprintf("Domain: %s (zone: %s)", subdomain, selectedZoneID))
			}
		}

		// --- Provision ---
		ui.Step(Verbose, "Calling Toggle API to launch EC2 instance")
		ui.Step(Verbose, fmt.Sprintf("Launch config: %s (%s)", selectedConfig.Name, selectedConfig.ID))
		ui.Step(Verbose, fmt.Sprintf("SSH keys: %d selected", len(selectedKeyIDs)))
		stop = ui.Spinner("Provisioning instance")
		var result struct {
			Instance     instance `json:"instance"`
			RodentNodeID string   `json:"rodent_node_id"`
			DomainName   string   `json:"domain_name"`
		}
		err = apiClient.Post(fmt.Sprintf("/projects/%s/cloud/instances", cfg.ProjectID), reqBody, &result)
		stop()
		if err != nil {
			// Clean up auto-managed SSH key on provision failure
			if useAutoKeys && autoKeySSHID != "" {
				apiClient.Delete(fmt.Sprintf("/projects/%s/cloud/ssh-keys/%s", cfg.ProjectID, autoKeySSHID), nil)
				if autoKeyPrivPath != "" {
					os.Remove(autoKeyPrivPath)
					os.Remove(autoKeyPrivPath + ".pub")
				}
			}
			return err
		}

		// Register auto-key mapping now that we have the instance ID
		if useAutoKeys && autoKeyPrivPath != "" {
			autokeys.Register(result.Instance.ID, autoKeyPrivPath, autoKeySSHID, autoKeySSHName)
		}

		ui.Success(fmt.Sprintf("Instance %q provisioned", result.Instance.Name))
		fmt.Println()
		fmt.Printf("  Instance ID:  %s\n", ui.Cyan(result.Instance.InstanceID))
		fmt.Printf("  State:        %s\n", stateColor(result.Instance.InstanceState))
		fmt.Printf("  Type:         %s\n", result.Instance.InstanceType)
		fmt.Printf("  Region:       %s\n", result.Instance.Region)

		addr := result.Instance.effectiveAddress()
		if addr != "" {
			fmt.Printf("  Address:      %s\n", ui.Cyan(addr))
		}
		if result.Instance.DomainName != "" {
			fmt.Printf("  Domain:       %s\n", ui.Cyan(result.Instance.DomainName))
		}

		// Poll until running (unless --no-wait)
		if !noWaitFlag && result.Instance.InstanceState != "running" {
			fmt.Println()
			ui.Step(Verbose, "Instance launched, waiting for running state (EIP association + boot)")
			stop = ui.Spinner("Waiting for instance to start")
			inst, err := pollInstanceStatus(result.Instance.ID, "running", 5*time.Minute)
			stop()
			if err != nil {
				ui.Warn(fmt.Sprintf("Instance may still be starting: %v", err))
			} else {
				ui.Success("Instance is running")
				addr = inst.effectiveAddress()
			}
		}

		if addr != "" {
			fmt.Println()
			ui.Info(fmt.Sprintf("Connect with: vctl instance ssh %s", name))
		}

		return nil
	},
}

var instanceStartCmd = &cobra.Command{
	Use:   "start <name-or-id>",
	Short: "Start a stopped instance",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireProject(); err != nil {
			return err
		}

		stop := ui.Spinner("Finding instance")
		inst, err := findInstance(args[0])
		stop()
		if err != nil {
			return err
		}

		stop = ui.Spinner(fmt.Sprintf("Starting %s", inst.Name))
		var result struct {
			Message  string   `json:"message"`
			Instance instance `json:"instance"`
		}
		err = apiClient.Post(fmt.Sprintf("/projects/%s/cloud/instances/%s/start", cfg.ProjectID, inst.ID), nil, &result)
		stop()

		if err != nil {
			return err
		}

		ui.Success(fmt.Sprintf("Instance %s starting", inst.Name))

		// Poll until running
		stop = ui.Spinner("Waiting for running state")
		final, err := pollInstanceStatus(inst.ID, "running", 5*time.Minute)
		stop()

		if err != nil {
			ui.Warn(fmt.Sprintf("Instance may still be starting: %v", err))
		} else {
			ui.Success(fmt.Sprintf("Instance %s is running", final.Name))
			if addr := final.effectiveAddress(); addr != "" {
				ui.Info(fmt.Sprintf("Address: %s", addr))
			}
		}

		return nil
	},
}

var instanceStopCmd = &cobra.Command{
	Use:   "stop <name-or-id>",
	Short: "Stop a running instance",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireProject(); err != nil {
			return err
		}

		stop := ui.Spinner("Finding instance")
		inst, err := findInstance(args[0])
		stop()
		if err != nil {
			return err
		}

		stopSpinner := ui.Spinner(fmt.Sprintf("Stopping %s", inst.Name))
		var result struct {
			Message  string   `json:"message"`
			Instance instance `json:"instance"`
		}
		err = apiClient.Post(fmt.Sprintf("/projects/%s/cloud/instances/%s/stop", cfg.ProjectID, inst.ID), nil, &result)
		stopSpinner()

		if err != nil {
			return err
		}

		ui.Success(fmt.Sprintf("Instance %s is stopping", inst.Name))
		return nil
	},
}

var instanceTerminateCmd = &cobra.Command{
	Use:   "terminate <name-or-id>",
	Short: "Terminate an instance (irreversible)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireProject(); err != nil {
			return err
		}

		stop := ui.Spinner("Finding instance")
		inst, err := findInstance(args[0])
		stop()
		if err != nil {
			return err
		}

		force, _ := cmd.Flags().GetBool("force")
		if !force {
			if !ui.Confirm(fmt.Sprintf("Terminate instance %q? This is irreversible", inst.Name)) {
				ui.Info("Cancelled")
				return nil
			}
		}

		stop = ui.Spinner(fmt.Sprintf("Terminating %s", inst.Name))
		var result struct {
			Message  string   `json:"message"`
			Warnings []string `json:"warnings"`
		}
		err = apiClient.Post(fmt.Sprintf("/projects/%s/cloud/instances/%s/terminate", cfg.ProjectID, inst.ID), nil, &result)
		stop()

		if err != nil {
			return err
		}

		ui.Success(fmt.Sprintf("Instance %s terminated", inst.Name))
		for _, w := range result.Warnings {
			ui.Warn(w)
		}

		// Clean up auto-generated SSH key (by instance ID, not name)
		if sshKeyID, ok := autokeys.GetSSHKeyID(inst.ID); ok {
			apiClient.Delete(fmt.Sprintf("/projects/%s/cloud/ssh-keys/%s", cfg.ProjectID, sshKeyID), nil)
			autokeys.RemoveKeyForInstance(inst.ID)
			ui.Info("Auto-managed SSH key cleaned up")
		}

		return nil
	},
}

var instanceStatusCmd = &cobra.Command{
	Use:   "status <name-or-id>",
	Short: "Refresh and show instance status",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireProject(); err != nil {
			return err
		}

		stop := ui.Spinner("Finding instance")
		inst, err := findInstance(args[0])
		stop()
		if err != nil {
			return err
		}

		// Refresh status from AWS
		stop = ui.Spinner("Refreshing status")
		var refreshed instance
		err = apiClient.Get(fmt.Sprintf("/projects/%s/cloud/instances/%s/status", cfg.ProjectID, inst.ID), &refreshed)
		stop()

		if err != nil {
			return err
		}

		fmt.Printf("  Name:          %s\n", ui.Bold(refreshed.Name))
		fmt.Printf("  State:         %s\n", stateColor(refreshed.InstanceState))
		fmt.Printf("  Instance ID:   %s\n", refreshed.InstanceID)
		fmt.Printf("  Type:          %s\n", refreshed.InstanceType)
		fmt.Printf("  Region:        %s\n", refreshed.Region)
		if refreshed.AvailabilityZone != "" {
			fmt.Printf("  AZ:            %s\n", refreshed.AvailabilityZone)
		}
		if refreshed.PublicIP != "" {
			fmt.Printf("  Public IP:     %s\n", refreshed.PublicIP)
		}
		if refreshed.PrivateIP != "" {
			fmt.Printf("  Private IP:    %s\n", refreshed.PrivateIP)
		}
		if refreshed.ElasticIPAddress != "" {
			fmt.Printf("  Elastic IP:    %s\n", ui.Cyan(refreshed.ElasticIPAddress))
		}
		if refreshed.DomainEnabled && refreshed.DomainName != "" {
			fmt.Printf("  Domain:        %s\n", ui.Cyan(refreshed.DomainName))
		}

		return nil
	},
}

// computeLocalKeyFingerprint computes the MD5 fingerprint of a public key file
// using the same algorithm as the Toggle server (raw MD5 hex of decoded key data).
func computeLocalKeyFingerprint(pubKeyPath string) (string, error) {
	data, err := os.ReadFile(pubKeyPath)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return "", fmt.Errorf("invalid public key format")
	}
	keyData, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return "", err
	}
	hash := md5.Sum(keyData)
	return fmt.Sprintf("%x", hash), nil
}

// findMatchingLocalKey finds a local SSH private key that matches one of the project SSH keys.
func findMatchingLocalKey(projectKeys []sshKey) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	sshDir := filepath.Join(home, ".ssh")

	pubFiles, err := filepath.Glob(filepath.Join(sshDir, "*.pub"))
	if err != nil {
		return "", err
	}

	for _, pubFile := range pubFiles {
		fp, err := computeLocalKeyFingerprint(pubFile)
		if err != nil {
			continue
		}
		for _, pk := range projectKeys {
			if fp == pk.Fingerprint {
				// Return the private key path (without .pub)
				privKey := strings.TrimSuffix(pubFile, ".pub")
				if _, err := os.Stat(privKey); err == nil {
					return privKey, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no matching local SSH key found in %s", sshDir)
}

var instanceSSHCmd = &cobra.Command{
	Use:   "ssh [name-or-id] [-- extra-ssh-args...]",
	Short: "SSH into an instance",
	Long: `SSH into an instance. Uses the default instance if no name is given.
Extra arguments after -- are passed to ssh.

Examples:
  vctl instance ssh                              # uses default instance
  vctl instance ssh mybox
  vctl instance ssh mybox -- -L 8080:localhost:8080
  vctl instance ssh mybox -- -N -D 1080
  vctl instance ssh -- hostname                  # run command on default instance`,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireProject(); err != nil {
			return err
		}

		user, _ := cmd.Flags().GetString("user")

		// Determine instance name and extra SSH args.
		// Try first arg as instance name; if it fails and a default is set,
		// treat all args as the remote command on the default instance.
		var extraSSHArgs []string

		stop := ui.Spinner("Finding instance")
		var inst *instance
		var err error
		if len(args) > 0 {
			inst, err = findInstance(args[0])
			if err != nil && cfg.InstanceName != "" {
				// First arg isn't an instance — use default, treat all args as SSH args
				inst, err = findInstance(cfg.InstanceName)
				extraSSHArgs = args
			} else {
				extraSSHArgs = args[1:]
			}
		} else if cfg.InstanceName != "" {
			inst, err = findInstance(cfg.InstanceName)
		} else {
			stop()
			return fmt.Errorf("no instance specified and no default set. Run 'vctl instance use <name>' first")
		}
		stop()
		if err != nil {
			return err
		}

		addr := inst.effectiveAddress()
		if addr == "" {
			return fmt.Errorf("instance %q has no public address (state: %s)", inst.Name, inst.InstanceState)
		}

		// Find SSH key: check vctl auto-managed keys first (by instance ID), then ~/.ssh/
		if keyPath, ok := autokeys.FindKeyForInstance(inst.ID); ok {
			ui.Info(fmt.Sprintf("Using auto-managed key: %s", keyPath))
			sshBin, err := findBinary("ssh")
			if err != nil {
				return err
			}
			sshArgs := []string{"ssh", "-o", "StrictHostKeyChecking=no", "-i", keyPath}
			var remoteCmd []string
			for _, arg := range extraSSHArgs {
				if strings.HasPrefix(arg, "-") {
					sshArgs = append(sshArgs, arg)
				} else {
					remoteCmd = append(remoteCmd, arg)
				}
			}
			sshArgs = append(sshArgs, user+"@"+addr)
			sshArgs = append(sshArgs, remoteCmd...)
			return syscall.Exec(sshBin, sshArgs, os.Environ())
		}

		// Fall back to matching project keys against ~/.ssh/
		var keys []sshKey
		err = apiClient.Get(fmt.Sprintf("/projects/%s/cloud/ssh-keys", cfg.ProjectID), &keys)
		if err != nil {
			return fmt.Errorf("failed to fetch project SSH keys: %w", err)
		}

		sshArgs := []string{"ssh"}
		keyPath, err := findMatchingLocalKey(keys)
		if err != nil {
			ui.Warn(fmt.Sprintf("Could not find matching SSH key: %v", err))
			ui.Info("Trying without specifying a key file...")
		} else {
			ui.Info(fmt.Sprintf("Using key: %s", keyPath))
			sshArgs = append(sshArgs, "-i", keyPath)
		}

		sshArgs = append(sshArgs, "-o", "StrictHostKeyChecking=no")
		var fallbackRemoteCmd []string
		for _, arg := range extraSSHArgs {
			if strings.HasPrefix(arg, "-") {
				sshArgs = append(sshArgs, arg)
			} else {
				fallbackRemoteCmd = append(fallbackRemoteCmd, arg)
			}
		}
		sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", user, addr))
		sshArgs = append(sshArgs, fallbackRemoteCmd...)

		// Find ssh binary
		sshBin, err := findBinary("ssh")
		if err != nil {
			return fmt.Errorf("ssh not found in PATH: %w", err)
		}

		ui.Info(fmt.Sprintf("Connecting to %s@%s", user, addr))
		return syscall.Exec(sshBin, sshArgs, os.Environ())
	},
}

// findBinary locates a binary in PATH.
func findBinary(name string) (string, error) {
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		full := filepath.Join(dir, name)
		if _, err := os.Stat(full); err == nil {
			return full, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH", name)
}

var instanceUseCmd = &cobra.Command{
	Use:   "use <name-or-id>",
	Short: "Set the default instance",
	Long:  "Set the default instance used by service commands.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireProject(); err != nil {
			return err
		}

		stop := ui.Spinner("Resolving instance")
		inst, err := findInstance(args[0])
		stop()
		if err != nil {
			return err
		}

		cfg.InstanceID = inst.ID
		cfg.InstanceName = inst.Name
		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		ui.Success(fmt.Sprintf("Default instance set to %s (%s)", ui.Bold(inst.Name), inst.ID))
		return nil
	},
}

func init() {
	instanceProvisionCmd.Flags().StringP("name", "n", "", "Instance name")
	instanceProvisionCmd.Flags().StringP("launch-config", "l", "", "Launch config ID or name")
	instanceProvisionCmd.Flags().StringP("ssh-keys", "k", "", "Comma-separated SSH key names or IDs")
	instanceProvisionCmd.Flags().StringP("instance-type", "t", "", "Override instance type")
	instanceProvisionCmd.Flags().String("domain", "", "Domain subdomain (default: instance name)")
	instanceProvisionCmd.Flags().String("hosted-zone", "", "Route53 hosted zone ID (default: first available)")
	instanceProvisionCmd.Flags().Bool("no-domain", false, "Skip domain provisioning")
	instanceProvisionCmd.Flags().Bool("auto-keys", false, "Auto-generate and manage SSH keypair (implied when --ssh-keys not provided)")
	instanceProvisionCmd.Flags().Bool("no-wait", false, "Don't wait for instance to reach running state")
	instanceProvisionCmd.Flags().Bool("spot", false, "Force spot instance (overrides launch config)")
	instanceProvisionCmd.Flags().Bool("on-demand", false, "Force on-demand instance (overrides launch config)")

	instanceTerminateCmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
	instanceSSHCmd.Flags().StringP("user", "u", "ubuntu", "SSH user")

	instanceCmd.AddCommand(instanceListCmd)
	instanceCmd.AddCommand(instanceProvisionCmd)
	instanceCmd.AddCommand(instanceStartCmd)
	instanceCmd.AddCommand(instanceStopCmd)
	instanceCmd.AddCommand(instanceTerminateCmd)
	instanceCmd.AddCommand(instanceStatusCmd)
	instanceCmd.AddCommand(instanceSSHCmd)
	instanceCmd.AddCommand(instanceUseCmd)
	rootCmd.AddCommand(instanceCmd)
}
