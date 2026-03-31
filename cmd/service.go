package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	autokeys "github.com/smallest-inc/velocity-cli/internal/keys"
	remotessh "github.com/smallest-inc/velocity-cli/internal/ssh"
	"github.com/smallest-inc/velocity-cli/internal/ui"
	"github.com/smallest-inc/velocity-cli/internal/velocity"
	"github.com/spf13/cobra"
)

// serviceContext holds the resolved state needed by every service subcommand.
type serviceContext struct {
	spec    *velocity.ProjectSpec
	devOver *velocity.DevOverrides
	inst    *instance
	keyPath string
	user    string
	addr    string
	logPath string
	pidPath string
}

// resolveService loads velocity.yml, resolves the target instance, and finds an SSH key.
func resolveService(cmd *cobra.Command) (*serviceContext, error) {
	if err := requireProject(); err != nil {
		return nil, err
	}

	spec, devOver, err := velocity.LoadFromCwd()
	if err != nil {
		return nil, err
	}

	// Resolve instance: --instance flag > velocity.dev.yml > config.InstanceID
	instanceFlag, _ := cmd.Flags().GetString("instance")
	instanceRef := instanceFlag
	if instanceRef == "" && devOver != nil && devOver.Instance != "" {
		instanceRef = devOver.Instance
	}
	if instanceRef == "" && cfg.InstanceID != "" {
		instanceRef = cfg.InstanceID
	}
	if instanceRef == "" {
		return nil, fmt.Errorf("no instance selected. Use 'vctl instance use <name>' or --instance")
	}

	stop := ui.Spinner("Resolving instance")
	inst, err := findInstance(instanceRef)
	stop()
	if err != nil {
		return nil, err
	}

	addr := inst.effectiveAddress()
	if addr == "" {
		return nil, fmt.Errorf("instance %q has no public address (state: %s)", inst.Name, inst.InstanceState)
	}

	// Resolve SSH key: auto-managed first, then manual match
	keyPath := ""
	if kp, ok := autokeys.FindKeyForInstance(inst.ID); ok {
		keyPath = kp
	} else {
		var keys []sshKey
		if err := apiClient.Get(fmt.Sprintf("/projects/%s/cloud/ssh-keys", cfg.ProjectID), &keys); err != nil {
			return nil, fmt.Errorf("failed to fetch project SSH keys: %w", err)
		}
		kp, err := findMatchingLocalKey(keys)
		if err != nil {
			return nil, fmt.Errorf("no SSH key found for instance %q: %w", inst.Name, err)
		}
		keyPath = kp
	}

	user := spec.Remote.User
	if user == "" {
		user = "ubuntu"
	}

	projectName := spec.Metadata.Name
	if projectName == "" {
		projectName = "velocity"
	}
	logPath := fmt.Sprintf("/tmp/velocity-%s.log", projectName)
	pidPath := fmt.Sprintf("/tmp/velocity-%s.pid", projectName)

	ui.Info(fmt.Sprintf("Instance: %s (%s)", ui.Bold(inst.Name), addr))
	ui.Step(Verbose, fmt.Sprintf("SSH key: %s", keyPath))
	ui.Step(Verbose, fmt.Sprintf("Remote user: %s", user))

	return &serviceContext{
		spec:    spec,
		devOver: devOver,
		inst:    inst,
		keyPath: keyPath,
		user:    user,
		addr:    addr,
		logPath: logPath,
		pidPath: pidPath,
	}, nil
}

// --- service command group ---

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage services on a remote instance",
	Long:  "Commands for syncing, starting, stopping, and managing services defined in velocity.yml.",
}

// runSync rsyncs the current working directory to the remote instance.
func runSync(ctx *serviceContext) error {
	remotePath := ctx.spec.Remote.Path
	if remotePath == "" {
		return fmt.Errorf("remote.path is not set in velocity.yml")
	}

	// Ensure remote path ends with /
	if !strings.HasSuffix(remotePath, "/") {
		remotePath += "/"
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	// Ensure source ends with /
	src := cwd
	if !strings.HasSuffix(src, "/") {
		src += "/"
	}

	rsyncArgs := []string{
		"rsync", "-avz", "--progress", "--delete",
	}

	// Include hidden files before exclude rules
	for _, inc := range ctx.spec.Sync.IncludeHidden {
		rsyncArgs = append(rsyncArgs, "--include", inc)
		// Also include patterns like .env.* if the include is .env
		if !strings.Contains(inc, "*") {
			rsyncArgs = append(rsyncArgs, "--include", inc+".*")
		}
	}

	// Exclude patterns
	for _, exc := range ctx.spec.Sync.Exclude {
		rsyncArgs = append(rsyncArgs, "--exclude", exc)
	}

	// SSH options
	sshOpt := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -i %s", ctx.keyPath)
	rsyncArgs = append(rsyncArgs, "-e", sshOpt)

	// Source and destination
	remoteSpec := fmt.Sprintf("%s@%s:%s", ctx.user, ctx.addr, remotePath)
	rsyncArgs = append(rsyncArgs, src, remoteSpec)

	ui.Info(fmt.Sprintf("Syncing to %s:%s", ctx.inst.Name, remotePath))
	ui.Step(Verbose, fmt.Sprintf("rsync %s", strings.Join(rsyncArgs[1:], " ")))

	rsyncCmd := exec.Command(rsyncArgs[0], rsyncArgs[1:]...)
	rsyncCmd.Stdout = os.Stdout
	rsyncCmd.Stderr = os.Stderr
	if err := rsyncCmd.Run(); err != nil {
		return fmt.Errorf("rsync failed: %w", err)
	}

	ui.Success("Sync complete")
	return nil
}

// --- service sync ---

var serviceSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Rsync project files to the remote instance",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, err := resolveService(cmd)
		if err != nil {
			return err
		}
		return runSync(ctx)
	},
}

// --- service up ---

var serviceUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Start dependencies, run setup, and launch services",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, err := resolveService(cmd)
		if err != nil {
			return err
		}

		remotePath := ctx.spec.Remote.Path
		skipSetup, _ := cmd.Flags().GetBool("skip-setup")
		skipSync, _ := cmd.Flags().GetBool("skip-sync")

		// 1. Sync project files to remote
		if !skipSync {
			if err := runSync(ctx); err != nil {
				return err
			}
		}

		// 1b. Rewrite localhost URLs in .env files to use the instance domain
		if ctx.inst.DomainEnabled && ctx.inst.DomainName != "" {
			domain := "https://" + ctx.inst.DomainName

			// For each service that has a .env, create .env.local with localhost URLs rewritten
			for svcName, svc := range ctx.spec.Services {
				svcPath := svc.Path
				if strings.HasPrefix(svcPath, "./") {
					svcPath = svcPath[2:]
				}
				envPath := fmt.Sprintf("%s/%s/.env", remotePath, svcPath)

				// Read existing .env from remote
				envContent, err := remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, fmt.Sprintf("cat %s 2>/dev/null", envPath))
				if err != nil || envContent == "" {
					continue
				}

				// Apply env_transforms first (before localhost rewrite)
				modified := envContent
				for _, t := range ctx.spec.Sync.EnvTransforms {
					// Skip if scoped to specific services and this isn't one
					if len(t.Services) > 0 {
						scoped := false
						for _, s := range t.Services {
							if s == svcName {
								scoped = true
								break
							}
						}
						if !scoped {
							continue
						}
					}
					// Enable multiline mode so ^ and $ match line boundaries
					pattern := t.Match
					if !strings.HasPrefix(pattern, "(?m)") {
						pattern = "(?m)" + pattern
					}
					re, err := regexp.Compile(pattern)
					if err != nil {
						ui.Warn(fmt.Sprintf("Invalid env_transform pattern %q: %v", t.Match, err))
						continue
					}
					// Expand template variables in replacement
					repl := t.Replace
					repl = strings.ReplaceAll(repl, "{{.Remote.User}}", ctx.user)
					repl = strings.ReplaceAll(repl, "{{.Remote.Path}}", remotePath)
					if ctx.inst.DomainName != "" {
						repl = strings.ReplaceAll(repl, "{{.Domain}}", ctx.inst.DomainName)
					}
					modified = re.ReplaceAllString(modified, repl)
				}

				// Then replace http://localhost:PORT → https://domain for declared prefixes
				localhostRe := regexp.MustCompile(`http://localhost:\d+`)
				rewritePrefixes := ctx.spec.Sync.EnvRewriteVars
				var rewrittenLines []string
				for _, line := range strings.Split(modified, "\n") {
					if len(rewritePrefixes) > 0 {
						for _, prefix := range rewritePrefixes {
							if strings.HasPrefix(line, prefix) {
								line = localhostRe.ReplaceAllString(line, domain)
								break
							}
						}
					} else {
						line = localhostRe.ReplaceAllString(line, domain)
					}
					rewrittenLines = append(rewrittenLines, line)
				}
				modified = strings.Join(rewrittenLines, "\n")

				changed := modified != envContent
				if !changed {
					continue
				}

				// Overwrite .env in-place on remote (sync restores it from local next time)
				writeCmd := fmt.Sprintf("cat > %s << 'ENVEOF'\n%s\nENVEOF", envPath, modified)
				if _, err := remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, writeCmd); err != nil {
					ui.Warn(fmt.Sprintf("Failed to write .env.local for %s: %v", svcName, err))
				} else {
					ui.Step(Verbose, fmt.Sprintf("%s: rewrote localhost URLs → %s", svcName, ctx.inst.DomainName))
				}
			}
			ui.Success(fmt.Sprintf("Env rewritten for %s", ctx.inst.DomainName))
		}

		// 2. Check and install runtimes
		if !skipSetup && len(ctx.spec.Runtime) > 0 {
			ui.Info("Checking runtimes...")
			// Source profile.d scripts so PATH includes previously-installed runtimes
			pathPrefix := "for f in /etc/profile.d/*.sh; do . \"$f\" 2>/dev/null; done; "
			for _, rt := range ctx.spec.Runtime {
				if rt.Check == "" {
					continue
				}
				// Run check command to see if runtime is present
				checkCmd := fmt.Sprintf("%s%s 2>/dev/null && echo 'OK' || echo 'MISSING'", pathPrefix, rt.Check)
				out, _ := remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, checkCmd)
				if strings.Contains(out, "OK") {
					ui.Step(Verbose, fmt.Sprintf("%s: installed", rt.Name))
					continue
				}

				if rt.Install == "" {
					ui.Warn(fmt.Sprintf("%s not found and no install command defined", rt.Name))
					continue
				}

				label := rt.Name
				if rt.Version != "" {
					label += " " + rt.Version
				}
				ui.Info(fmt.Sprintf("Installing %s...", label))
				installCmd := pathPrefix + rt.Install
				if err := remotessh.ExecStream(ctx.keyPath, ctx.user, ctx.addr, installCmd); err != nil {
					return fmt.Errorf("failed to install %s: %w", rt.Name, err)
				}
				ui.Success(fmt.Sprintf("%s installed", label))
			}
		}

		// 3. Start Docker dependencies (check if already running)
		if !skipSetup && len(ctx.spec.Dependencies.Docker) > 0 {
			ui.Info("Checking Docker dependencies...")
			for _, dep := range ctx.spec.Dependencies.Docker {
				// Check container state: running, stopped (exists but not running), or absent
				checkCmd := fmt.Sprintf(
					"if docker ps --format '{{.Names}}' | grep -q '^%s$'; then echo running; "+
						"elif docker ps -a --format '{{.Names}}' | grep -q '^%s$'; then echo stopped; "+
						"else echo absent; fi",
					dep.Name, dep.Name)
				out, _ := remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, checkCmd)
				state := strings.TrimSpace(out)

				if state == "running" {
					ui.Step(Verbose, fmt.Sprintf("%s: already running", dep.Name))
					continue
				}

				if state == "stopped" {
					// Container exists but stopped — start it (preserves volumes/data)
					ui.Step(Verbose, fmt.Sprintf("Restarting stopped container: %s", dep.Name))
					stop := ui.Spinner(fmt.Sprintf("Starting %s", dep.Name))
					_, err := remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, fmt.Sprintf("docker start %s", dep.Name))
					stop()
					if err != nil {
						ui.Warn(fmt.Sprintf("Failed to start %s: %v", dep.Name, err))
					} else {
						ui.Success(fmt.Sprintf("Container %s started (existing data preserved)", dep.Name))
					}
					continue
				}

				// Container doesn't exist — create with named volume for data persistence
				ui.Step(Verbose, fmt.Sprintf("Creating container: %s (%s)", dep.Name, dep.Image))
				dockerCmd := fmt.Sprintf("docker run -d --name %s --restart unless-stopped", dep.Name)
				if dep.Platform != "" {
					dockerCmd += fmt.Sprintf(" --platform %s", dep.Platform)
				}
				for _, port := range dep.Ports {
					dockerCmd += fmt.Sprintf(" -p %s", port)
				}
				for k, v := range dep.Env {
					dockerCmd += fmt.Sprintf(" -e %s=%s", k, v)
				}
				dockerCmd += fmt.Sprintf(" %s", dep.Image)

				stop := ui.Spinner(fmt.Sprintf("Starting %s", dep.Name))
				_, err := remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, dockerCmd)
				stop()
				if err != nil {
					ui.Warn(fmt.Sprintf("Failed to start %s: %v", dep.Name, err))
				} else {
					ui.Success(fmt.Sprintf("Container %s started", dep.Name))
				}
			}
		}

		// Ensure installed runtimes are in PATH for setup/start commands
		envPrefix := "for f in /etc/profile.d/*.sh; do . \"$f\" 2>/dev/null; done; "

		// 4. Run lifecycle.setup (always runs — npm install and turbo build are idempotent)
		if !skipSetup && ctx.spec.Lifecycle.Setup != "" {
			ui.Info(fmt.Sprintf("Running setup: %s", ctx.spec.Lifecycle.Setup))
			setupCmd := fmt.Sprintf("%scd %s && %s", envPrefix, remotePath, ctx.spec.Lifecycle.Setup)
			if err := remotessh.ExecStream(ctx.keyPath, ctx.user, ctx.addr, setupCmd); err != nil {
				return fmt.Errorf("setup failed: %w", err)
			}
			ui.Success("Setup complete")
		}

		// 5. Run lifecycle.start
		if ctx.spec.Lifecycle.Start != "" {
			detach, _ := cmd.Flags().GetBool("detach")
			if detach {
				// Background mode: nohup + log file
				ui.Info(fmt.Sprintf("Starting services (detached): %s", ctx.spec.Lifecycle.Start))
				// Use setsid to fully detach the process from the SSH session
				// Save the PID to a file so `down` can kill the entire process group
				startCmd := fmt.Sprintf(
					"%scd %s && (setsid bash -c '%s' > %s 2>&1 < /dev/null & PID=$!; echo $PID > %s; echo $PID) 2>/dev/null",
					envPrefix, remotePath, ctx.spec.Lifecycle.Start, ctx.logPath, ctx.pidPath,
				)
				out, err := remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, startCmd)
				if err != nil {
					return fmt.Errorf("failed to start services: %w", err)
				}
				ui.Success(fmt.Sprintf("Services started (PID: %s)", strings.TrimSpace(out)))
				ui.Info("Logs: vctl service logs")

				// Deploy Traefik in detach mode
				hasRoutes := false
				for _, svc := range ctx.spec.Services {
					if len(svc.Routes) > 0 {
						hasRoutes = true
						break
					}
				}
				if hasRoutes && ctx.inst.DomainEnabled && ctx.inst.DomainName != "" {
					ui.Info("Deploying Traefik routing config...")
					if err := deployTraefik(ctx); err != nil {
						ui.Warn(fmt.Sprintf("Traefik deployment failed: %v", err))
					} else {
						ui.Success("Traefik routing configured")
					}
				}
			} else {
				// Foreground mode: stream output live (Ctrl+C to stop)
				// Deploy Traefik first since we won't return from streaming
				hasRoutes := false
				for _, svc := range ctx.spec.Services {
					if len(svc.Routes) > 0 {
						hasRoutes = true
						break
					}
				}
				if hasRoutes && ctx.inst.DomainEnabled && ctx.inst.DomainName != "" {
					ui.Info("Deploying Traefik routing config...")
					if err := deployTraefik(ctx); err != nil {
						ui.Warn(fmt.Sprintf("Traefik deployment failed: %v", err))
					} else {
						ui.Success("Traefik routing configured")
					}
				}

				fmt.Println()
				ui.Info(fmt.Sprintf("Starting services: %s", ctx.spec.Lifecycle.Start))
				ui.Info("Press Ctrl+C to stop")
				fmt.Println()

				startCmd := fmt.Sprintf("%scd %s && %s", envPrefix, remotePath, ctx.spec.Lifecycle.Start)
				return remotessh.ExecStream(ctx.keyPath, ctx.user, ctx.addr, startCmd)
			}
		}

		fmt.Println()
		ui.Success("Service environment is up")
		return nil
	},
}

// --- service down ---

var serviceDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop the dev process (and optionally Docker dependencies)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, err := resolveService(cmd)
		if err != nil {
			return err
		}

		all, _ := cmd.Flags().GetBool("all")

		// Stop dev process
		if ctx.spec.Lifecycle.Stop != "" {
			ui.Info(fmt.Sprintf("Running stop command: %s", ctx.spec.Lifecycle.Stop))
			remotePath := ctx.spec.Remote.Path
			stopCmd := fmt.Sprintf("cd %s && %s", remotePath, ctx.spec.Lifecycle.Stop)
			if err := remotessh.ExecStream(ctx.keyPath, ctx.user, ctx.addr, stopCmd); err != nil {
				ui.Warn(fmt.Sprintf("Stop command failed: %v", err))
			} else {
				ui.Success("Dev process stopped")
			}
		}

		// Kill all service processes: PID group + port-based fuser as reliable cleanup
		if ctx.pidPath != "" {
			remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr,
				fmt.Sprintf("if [ -f %s ]; then PID=$(cat %s); kill -9 -- -$PID 2>/dev/null; rm -f %s; fi; true",
					ctx.pidPath, ctx.pidPath, ctx.pidPath))
		}
		// fuser -k on all declared service ports — most reliable way to ensure clean shutdown
		var fuserCmds []string
		for _, svc := range ctx.spec.Services {
			if svc.Port > 0 {
				fuserCmds = append(fuserCmds, fmt.Sprintf("fuser -k -9 %d/tcp 2>/dev/null", svc.Port))
			}
		}
		if len(fuserCmds) > 0 {
			remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, strings.Join(fuserCmds, "; ")+"; true")
		}

		// Stop Docker dependencies if --all
		if all && len(ctx.spec.Dependencies.Docker) > 0 {
			ui.Info("Stopping Docker dependencies...")
			for _, dep := range ctx.spec.Dependencies.Docker {
				stop := ui.Spinner(fmt.Sprintf("Stopping %s", dep.Name))
				_, err := remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, fmt.Sprintf("docker stop %s 2>/dev/null", dep.Name))
				stop()
				if err != nil {
					ui.Step(Verbose, fmt.Sprintf("%s: not running", dep.Name))
				} else {
					ui.Success(fmt.Sprintf("%s stopped (data preserved)", dep.Name))
				}
			}
		}

		// Stop Traefik if --all
		if all {
			remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, "cd /opt/traefik && docker compose down 2>/dev/null; true")
		}

		ui.Success("Environment is down")
		return nil
	},
}

// --- service reset ---

var serviceResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Clean slate: stop everything, remove containers, delete node_modules",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, err := resolveService(cmd)
		if err != nil {
			return err
		}

		remotePath := ctx.spec.Remote.Path

		// 1. Stop dev process
		if ctx.spec.Lifecycle.Stop != "" {
			remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, fmt.Sprintf("cd %s && %s 2>/dev/null; true", remotePath, ctx.spec.Lifecycle.Stop))
		}
		if ctx.pidPath != "" {
			remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr,
				fmt.Sprintf("if [ -f %s ]; then PID=$(cat %s); kill -9 -- -$PID 2>/dev/null; rm -f %s; fi; true",
					ctx.pidPath, ctx.pidPath, ctx.pidPath))
		}
		var resetFuserCmds []string
		for _, svc := range ctx.spec.Services {
			if svc.Port > 0 {
				resetFuserCmds = append(resetFuserCmds, fmt.Sprintf("fuser -k -9 %d/tcp 2>/dev/null", svc.Port))
			}
		}
		if len(resetFuserCmds) > 0 {
			remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, strings.Join(resetFuserCmds, "; ")+"; true")
		}
		ui.Success("Dev process stopped")

		// 2. Remove Docker containers and volumes
		if len(ctx.spec.Dependencies.Docker) > 0 {
			ui.Info("Removing Docker containers and volumes...")
			for _, dep := range ctx.spec.Dependencies.Docker {
				stop := ui.Spinner(fmt.Sprintf("Removing %s", dep.Name))
				remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, fmt.Sprintf("docker rm -f %s 2>/dev/null; true", dep.Name))
				stop()
				ui.Step(Verbose, fmt.Sprintf("%s removed", dep.Name))
			}
		}

		// 3. Stop and remove Traefik
		remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, "cd /opt/traefik && docker compose down -v 2>/dev/null; true")

		// 4. Delete node_modules, .turbo, .next, dist
		ui.Info("Cleaning build artifacts...")
		cleanCmd := fmt.Sprintf("cd %s && rm -rf node_modules .turbo .next dist apps/*/node_modules apps/*/.turbo apps/*/.next apps/*/dist packages/*/node_modules packages/*/.turbo packages/*/dist 2>/dev/null; echo done", remotePath)
		stop := ui.Spinner("Deleting node_modules and caches")
		remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, cleanCmd)
		stop()

		// 5. Remove log file
		remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, fmt.Sprintf("rm -f %s", ctx.logPath))

		fmt.Println()
		ui.Success("Clean slate. Run 'vctl service up' to rebuild from scratch.")
		return nil
	},
}

// --- service status ---

var serviceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check which service ports are listening on the instance",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, err := resolveService(cmd)
		if err != nil {
			return err
		}

		if len(ctx.spec.Services) == 0 {
			ui.Warn("No services defined in velocity.yml")
			return nil
		}

		// Collect ports in deterministic order
		type svcPort struct {
			name string
			port int
		}
		var svcs []svcPort
		for name, svc := range ctx.spec.Services {
			svcs = append(svcs, svcPort{name: name, port: svc.Port})
		}
		sort.Slice(svcs, func(i, j int) bool { return svcs[i].name < svcs[j].name })

		// Build port list
		var ports []string
		for _, sp := range svcs {
			ports = append(ports, fmt.Sprintf("%d", sp.port))
		}

		// Check which ports are listening
		checkCmd := fmt.Sprintf(
			`for port in %s; do echo -n "$port:"; lsof -iTCP:$port -sTCP:LISTEN -P -n 2>/dev/null | grep -c LISTEN; done`,
			strings.Join(ports, " "),
		)

		stop := ui.Spinner("Checking services")
		out, err := remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, checkCmd)
		stop()
		if err != nil {
			return fmt.Errorf("failed to check service status: %w", err)
		}

		// Parse results
		portStatus := make(map[string]bool)
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				count := strings.TrimSpace(parts[1])
				portStatus[parts[0]] = count != "0" && count != ""
			}
		}

		// Display table
		headers := []string{"SERVICE", "PORT", "STATUS"}
		var rows [][]string
		for _, sp := range svcs {
			portStr := fmt.Sprintf("%d", sp.port)
			status := ui.Red("down")
			if portStatus[portStr] {
				status = ui.Green("up")
			}
			rows = append(rows, []string{sp.name, portStr, status})
		}
		ui.Table(headers, rows)
		return nil
	},
}

// --- service logs ---

var serviceLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Tail the dev process log on the remote instance",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, err := resolveService(cmd)
		if err != nil {
			return err
		}

		ui.Info(fmt.Sprintf("Tailing %s on %s (Ctrl+C to stop)", ctx.logPath, ctx.inst.Name))
		tailCmd := fmt.Sprintf("tail -f %s", ctx.logPath)
		return remotessh.ExecInteractive(ctx.keyPath, ctx.user, ctx.addr, "-t", tailCmd)
	},
}

// --- service traefik ---

var serviceTraefikCmd = &cobra.Command{
	Use:   "traefik",
	Short: "Deploy Traefik reverse proxy config to the instance",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, err := resolveService(cmd)
		if err != nil {
			return err
		}

		if !ctx.inst.DomainEnabled || ctx.inst.DomainName == "" {
			return fmt.Errorf("instance %q does not have a domain configured", ctx.inst.Name)
		}

		if err := deployTraefik(ctx); err != nil {
			return err
		}

		ui.Success("Traefik routing configured")
		return nil
	},
}

// deployTraefik generates Traefik config files, SCPs them, and starts Traefik.
func deployTraefik(ctx *serviceContext) error {
	domain := ctx.inst.DomainName

	// Create temp directory for config files
	tmpDir, err := os.MkdirTemp("", "velocity-traefik-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// 1. Generate docker-compose.yml
	composeContent := `services:
  traefik:
    image: traefik:v3.3
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./traefik.yml:/etc/traefik/traefik.yml:ro
      - ./dynamic:/etc/traefik/dynamic:ro
      - ./acme:/etc/traefik/acme
    network_mode: host
`
	if err := os.WriteFile(filepath.Join(tmpDir, "docker-compose.yml"), []byte(composeContent), 0644); err != nil {
		return err
	}

	// 2. Generate traefik.yml (static config)
	traefikYml := `entryPoints:
  web:
    address: ":80"
    http:
      redirections:
        entryPoint:
          to: websecure
  websecure:
    address: ":443"

certificatesResolvers:
  letsencrypt:
    acme:
      email: developer@smallest.ai
      storage: /etc/traefik/acme/acme.json
      httpChallenge:
        entryPoint: web

providers:
  file:
    directory: /etc/traefik/dynamic
    watch: true
`
	if err := os.WriteFile(filepath.Join(tmpDir, "traefik.yml"), []byte(traefikYml), 0644); err != nil {
		return err
	}

	// 3. Generate dynamic/routes.yml from services
	dynamicDir := filepath.Join(tmpDir, "dynamic")
	if err := os.MkdirAll(dynamicDir, 0755); err != nil {
		return err
	}

	routesYml := generateRoutesYml(ctx.spec.Services, domain, ctx.spec.Network.AllowedIPs)
	if err := os.WriteFile(filepath.Join(dynamicDir, "routes.yml"), []byte(routesYml), 0644); err != nil {
		return err
	}

	// 4. SCP config to instance
	remoteTraefikDir := "/opt/traefik"

	ui.Step(Verbose, "Creating remote directory structure")
	mkdirCmd := fmt.Sprintf("sudo mkdir -p %s/dynamic %s/acme && sudo chown -R %s:%s %s",
		remoteTraefikDir, remoteTraefikDir, ctx.user, ctx.user, remoteTraefikDir)
	if _, err := remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, mkdirCmd); err != nil {
		return fmt.Errorf("failed to create remote directory: %w", err)
	}

	ui.Step(Verbose, "Copying Traefik config files")
	if err := remotessh.CopyToRemote(ctx.keyPath, ctx.user, ctx.addr,
		filepath.Join(tmpDir, "docker-compose.yml"),
		filepath.Join(remoteTraefikDir, "docker-compose.yml")); err != nil {
		return fmt.Errorf("failed to copy docker-compose.yml: %w", err)
	}

	if err := remotessh.CopyToRemote(ctx.keyPath, ctx.user, ctx.addr,
		filepath.Join(tmpDir, "traefik.yml"),
		filepath.Join(remoteTraefikDir, "traefik.yml")); err != nil {
		return fmt.Errorf("failed to copy traefik.yml: %w", err)
	}

	if err := remotessh.CopyToRemote(ctx.keyPath, ctx.user, ctx.addr,
		filepath.Join(dynamicDir, "routes.yml"),
		filepath.Join(remoteTraefikDir, "dynamic", "routes.yml")); err != nil {
		return fmt.Errorf("failed to copy routes.yml: %w", err)
	}

	// 5. Start/restart Traefik
	ui.Step(Verbose, "Starting Traefik")
	startCmd := fmt.Sprintf("cd %s && docker compose down 2>/dev/null; docker compose up -d", remoteTraefikDir)
	if _, err := remotessh.Exec(ctx.keyPath, ctx.user, ctx.addr, startCmd); err != nil {
		return fmt.Errorf("failed to start Traefik: %w", err)
	}

	ui.Info(fmt.Sprintf("Traefik routing configured for %s", ui.Cyan(domain)))
	return nil
}

// generateRoutesYml creates the Traefik dynamic routes configuration from velocity.yml services.
func generateRoutesYml(services map[string]velocity.Service, domain string, allowedIPs []string) string {
	var sb strings.Builder
	sb.WriteString("http:\n")

	// Collect services with routes in deterministic order
	type svcEntry struct {
		name    string
		service velocity.Service
	}
	var entries []svcEntry
	for name, svc := range services {
		entries = append(entries, svcEntry{name: name, service: svc})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	// Middlewares (IP allowlist if configured)
	hasAllowList := len(allowedIPs) > 0
	if hasAllowList {
		sb.WriteString("  middlewares:\n")
		sb.WriteString("    vpn-only:\n")
		sb.WriteString("      ipAllowList:\n")
		sb.WriteString("        sourceRange:\n")
		for _, ip := range allowedIPs {
			sb.WriteString(fmt.Sprintf("          - \"%s\"\n", ip))
		}
	}

	// Routers
	sb.WriteString("  routers:\n")
	for _, e := range entries {
		for ri, route := range e.service.Routes {
			routerName := e.name
			if ri > 0 {
				routerName = fmt.Sprintf("%s-%d", e.name, ri)
			}
			rule := fmt.Sprintf("Host(`%s`)", domain)
			if route.Path != "" {
				rule = fmt.Sprintf("Host(`%s`) && PathPrefix(`%s`)", domain, route.Path)
			}

			sb.WriteString(fmt.Sprintf("    %s:\n", routerName))
			sb.WriteString(fmt.Sprintf("      rule: \"%s\"\n", rule))
			sb.WriteString(fmt.Sprintf("      service: %s\n", e.name))
			sb.WriteString("      entryPoints:\n")
			sb.WriteString("        - websecure\n")
			sb.WriteString("      tls:\n")
			sb.WriteString("        certResolver: letsencrypt\n")
			if hasAllowList {
				sb.WriteString("      middlewares:\n")
				sb.WriteString("        - vpn-only\n")
			}
			if route.Priority > 0 {
				sb.WriteString(fmt.Sprintf("      priority: %d\n", route.Priority))
			}
		}
	}

	// Services
	sb.WriteString("  services:\n")
	for _, e := range entries {
		if len(e.service.Routes) > 0 {
			sb.WriteString(fmt.Sprintf("    %s:\n", e.name))
			sb.WriteString("      loadBalancer:\n")
			sb.WriteString("        servers:\n")
			sb.WriteString(fmt.Sprintf("          - url: \"http://127.0.0.1:%d\"\n", e.service.Port))
		}
	}

	return sb.String()
}

func init() {
	serviceCmd.PersistentFlags().StringP("instance", "i", "", "Target instance (name or ID)")

	serviceUpCmd.Flags().Bool("detach", false, "Run services in background (default: foreground with live output)")
	serviceUpCmd.Flags().Bool("skip-sync", false, "Skip file sync to remote")
	serviceUpCmd.Flags().Bool("skip-setup", false, "Skip runtimes, dependencies, and setup")

	serviceDownCmd.Flags().Bool("all", false, "Also stop Docker dependencies and Traefik")

	serviceCmd.AddCommand(serviceSyncCmd)
	serviceCmd.AddCommand(serviceUpCmd)
	serviceCmd.AddCommand(serviceDownCmd)
	serviceCmd.AddCommand(serviceResetCmd)
	serviceCmd.AddCommand(serviceStatusCmd)
	serviceCmd.AddCommand(serviceLogsCmd)
	serviceCmd.AddCommand(serviceTraefikCmd)
	rootCmd.AddCommand(serviceCmd)
}
