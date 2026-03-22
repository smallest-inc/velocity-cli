# vctl

CLI for provisioning and managing cloud instances via [Toggle](https://toggle.strata.foo).

## Install

```bash
# macOS (Apple Silicon)
curl -sL https://github.com/smallest-inc/velocity-cli/releases/latest/download/vctl_darwin_arm64.tar.gz | tar xz
sudo mv vctl /usr/local/bin/

# macOS (Intel)
curl -sL https://github.com/smallest-inc/velocity-cli/releases/latest/download/vctl_darwin_amd64.tar.gz | tar xz
sudo mv vctl /usr/local/bin/

# Linux (x86_64)
curl -sL https://github.com/smallest-inc/velocity-cli/releases/latest/download/vctl_linux_amd64.tar.gz | tar xz
sudo mv vctl /usr/local/bin/

# Linux (ARM64)
curl -sL https://github.com/smallest-inc/velocity-cli/releases/latest/download/vctl_linux_arm64.tar.gz | tar xz
sudo mv vctl /usr/local/bin/
```

Self-update: `vctl upgrade`

## Quick Start

```bash
# Authenticate with a personal access token
vctl auth login --token strata_pat_xxx

# Provision an instance (interactive)
vctl instance provision

# Provision an instance (non-interactive)
vctl instance provision --name my-server

# SSH into it
vctl instance ssh my-server

# Terminate
vctl instance terminate my-server
```

## Authentication

Create a PAT in the Toggle web UI at **Identity & Access > Access Tokens**, then:

```bash
vctl auth login --token strata_pat_xxx
```

This validates the token, saves it to `~/.vctl/credentials`, and auto-selects your project if you only have one.

Check status:

```bash
vctl auth status
```

Environment variables override saved config:

| Variable | Purpose |
|----------|---------|
| `VCTL_TOKEN` | PAT token |
| `VCTL_ENDPOINT` | Toggle API URL (default: `https://toggle.strata.foo`) |
| `VCTL_PROJECT` | Active project ID |

## Commands

### Projects

```bash
vctl project list              # List all projects
vctl project use <handle>      # Set active project
```

### Instances

```bash
vctl instance list                          # List all instances
vctl instance provision                     # Interactive provisioning
vctl instance provision --name prod-01      # Non-interactive (auto SSH keys, default launch config)
vctl instance status <name-or-id>           # Refresh status from AWS
vctl instance start <name-or-id>            # Start stopped instance
vctl instance stop <name-or-id>             # Stop running instance
vctl instance terminate <name-or-id>        # Terminate (with confirmation)
vctl instance terminate <name> --force      # Skip confirmation
vctl instance ssh <name-or-id>              # SSH into instance
vctl instance ssh <name> -- -L 8080:localhost:8080  # SSH with port forwarding
```

#### Provision Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--name` | `-n` | Instance name (required for non-interactive) |
| `--launch-config` | `-l` | Launch config name or ID (uses default if omitted) |
| `--ssh-keys` | `-k` | Comma-separated key names/IDs (auto-generates if omitted) |
| `--auto-keys` | | Force auto SSH key generation |
| `--instance-type` | `-t` | Override instance type (e.g. `t3.large`) |
| `--domain` | | Subdomain for Route53 DNS record |
| `--hosted-zone` | | Route53 hosted zone ID |
| `--no-wait` | | Don't wait for running state |

### SSH Keys

```bash
vctl key list                              # List project SSH keys
vctl key add my-laptop ~/.ssh/id_ed25519.pub   # Upload a public key
vctl key remove my-laptop                  # Delete a key
```

### Auto SSH Key Management

When no `--ssh-keys` flag is provided, vctl automatically:

1. Generates an ed25519 keypair
2. Uploads the public key to Toggle
3. Stores the private key in `~/.vctl/keys/`
4. Uses it for `vctl instance ssh`
5. Cleans up both local and remote keys on `vctl instance terminate`

To use a specific key instead: `--ssh-keys my-laptop`

### File Sync

```bash
vctl sync ./src my-server:/home/ubuntu/src     # Local to remote
vctl sync my-server:/var/log ./logs             # Remote to local
```

Uses `rsync` under the hood. Automatically resolves instance IP and SSH key.

### Launch Configs

```bash
vctl config list           # List available launch configurations
```

### Cloud Provider

```bash
vctl provider status       # Show AWS provider config and verification status
vctl provider setup        # Interactive setup: generate ExternalID, configure IAM role
```

## Non-Interactive / Agentic Mode

All commands work without interactive prompts when flags are provided:

```bash
# Full provisioning pipeline (zero prompts)
vctl auth login --token $PAT --endpoint https://toggle.strata.foo
vctl project use my-project
vctl instance provision --name worker-01 --launch-config "GPU Large" --no-wait
vctl instance status worker-01
vctl instance ssh worker-01 -- "nvidia-smi"
vctl instance terminate worker-01 --force
```

When stdin is not a TTY (piped input, CI, agent control), vctl automatically:
- Skips all interactive prompts
- Uses default launch config if `--launch-config` is omitted
- Auto-generates SSH keys if `--ssh-keys` is omitted
- Disables spinner animation (prints static status lines)

## Config

All state is stored in `~/.vctl/`:

```
~/.vctl/
├── config.yml          # Endpoint, active project
├── credentials         # PAT token (0600 permissions)
├── keys/               # Auto-generated SSH keypairs
│   ├── manifest.yml    # Maps instance IDs to key files
│   ├── vctl-a1b2c3...  # Private key
│   └── vctl-a1b2c3...pub
└── update_state.yml    # Auto-update check cache
```

## Shell Completion

```bash
# Zsh
echo 'source <(vctl completion zsh)' >> ~/.zshrc

# Bash
echo 'source <(vctl completion bash)' >> ~/.bashrc

# Fish
vctl completion fish | source
```

## Global Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--endpoint` | | Toggle API endpoint |
| `--token` | | PAT token (overrides saved credentials) |
| `--project` | | Project ID (overrides saved selection) |
| `--verbose` | `-v` | Show step-by-step execution details |
