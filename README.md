# vctl

CLI for provisioning and managing cloud dev instances via [Toggle](https://toggle.strata.foo).

## Install

```bash
curl -sL https://raw.githubusercontent.com/smallest-inc/velocity-cli/main/install.sh | bash
```

Or build from source:

```bash
make install   # builds and installs to /usr/local/bin/vctl
```

Self-update: `vctl upgrade`

## Quick Start

```bash
# Authenticate
vctl auth login --token strata_pat_xxx

# Provision an instance
vctl instance provision --name my-dev

# Bring up the full dev environment (from project root with velocity.yml)
vctl service up

# Stop everything
vctl service down

# Terminate the instance
vctl instance terminate my-dev
```

## How It Works

vctl manages the full developer workflow: provision a cloud instance, sync your code, install runtimes, start dependencies, and run your services — all with a single `vctl service up`.

```
┌─────────────┐    ┌──────────────┐    ┌──────────────────────────┐
│  Local repo  │───▶│  vctl up     │───▶│  Remote EC2 instance     │
│  velocity.yml│    │              │    │                          │
│  .env files  │    │  1. sync     │    │  runtimes (node,go,etc)  │
│  app code    │    │  2. env      │    │  docker deps (redis,pg)  │
│              │    │  3. runtimes │    │  app services (turbo)    │
│              │    │  4. deps     │    │  traefik (ssl + routing) │
│              │    │  5. setup    │    │                          │
│              │    │  6. start    │    │  https://my.dev.smll.ai  │
│              │    │  7. traefik  │    │                          │
└─────────────┘    └──────────────┘    └──────────────────────────┘
```

## velocity.yml

The project spec file. Defines everything vctl needs to set up a dev environment.

### Example (atoms-platform)

```yaml
apiVersion: velocity/v1
kind: Project

metadata:
  name: atoms-platform
  description: Atoms voice AI platform — monorepo with 6 services
  team: atoms

remote:
  path: /home/ubuntu/atoms-platform
  user: ubuntu

runtime:
  - name: node
    version: "22"
    check: node --version
    install: |
      curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
      sudo apt-get install -y nodejs

  - name: go
    version: "1.24"
    check: go version
    install: |
      curl -fsSL https://go.dev/dl/go1.24.1.linux-arm64.tar.gz | sudo tar -C /usr/local -xzf -
      echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' | sudo tee /etc/profile.d/go.sh
      grep -q '/usr/local/go/bin' ~/.bashrc || echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc
      export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin

  - name: air
    check: which air
    install: go install github.com/air-verse/air@latest

  - name: rds-ca-bundle
    check: test -f ~/.ssl/rds-global-bundle.pem
    install: |
      mkdir -p ~/.ssl
      curl -fsSL https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem -o ~/.ssl/rds-global-bundle.pem

  - name: docker
    check: docker --version
    install: |
      curl -fsSL https://get.docker.com | sudo sh
      sudo usermod -aG docker $USER

services:
  atoms-frontend:
    path: ./apps/atoms
    port: 3001
    routes:
      - path: /
        priority: 0
  main-backend:
    path: ./apps/main-backend
    port: 4001
    routes:
      - path: /api/v1/
  console-backend:
    path: ./apps/console-backend
    port: 4000
    routes:
      - path: /console/

lifecycle:
  setup: set -a && source apps/atoms/.env && npm install
  start: export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin && npm run dev
  stop: npx turbo daemon stop

sync:
  exclude:
    - node_modules
    - .turbo
    - .next
    - dist
    - .git
    - "*.log"
  include_hidden:
    - .env
    - .env.*
  env_rewrite_vars:
    - NEXT_PUBLIC_
    - ORIGIN
    - ATOMS_FRONTEND_URL
    - CONSOLE_FRONTEND_URL
  env_transforms:
    - match: "%2FUsers%2F[^&]*\\.pem"
      replace: "%2Fhome%2F{{.Remote.User}}%2F.ssl%2Frds-global-bundle.pem"
    - match: "^NODE_ENV=.*"
      replace: "NODE_ENV=development"
      services: [console-backend, atoms-frontend]

dependencies:
  docker:
    - name: rabbitmq
      image: rabbitmq:3-management
      ports:
        - "5672:5672"
        - "15672:15672"
    - name: payment-pg
      image: postgres:16
      ports:
        - "5432:5432"
      env:
        POSTGRES_USER: payment_svc
        POSTGRES_PASSWORD: localdev
        POSTGRES_DB: payment_db
    - name: redis-cluster
      image: redis:7
      ports:
        - "6379:6379"

network:
  allowed_ips:
    - "65.2.141.169/32"  # OpenVPN server
```

### Spec Reference

| Section | Purpose |
|---------|---------|
| `metadata` | Project name, description, team |
| `remote` | Remote path and SSH user on the instance |
| `runtime` | Dependencies to check/install (node, go, docker, etc.) |
| `services` | Service definitions with ports and Traefik routes |
| `lifecycle` | Commands for setup, start, stop |
| `sync` | rsync exclude/include patterns |
| `sync.env_rewrite_vars` | Env var prefixes to rewrite `localhost:PORT` → instance domain |
| `sync.env_transforms` | Regex replacements on .env files (with optional service scoping) |
| `dependencies.docker` | Docker containers to run (managed with start/stop, data preserved) |
| `network.allowed_ips` | Traefik IP allowlist for HTTPS routes |

### Env Rewrite

When `service up` runs on a domain-enabled instance, vctl automatically rewrites `.env` files:

1. **`env_rewrite_vars`** — For each declared prefix (e.g. `NEXT_PUBLIC_`), replaces `http://localhost:PORT` with `https://{instance-domain}`. Only browser-facing vars are rewritten; backend-to-backend URLs stay as localhost.

2. **`env_transforms`** — Regex-based replacements for things like TLS cert paths. Supports `{{.Remote.User}}` and `{{.Remote.Path}}` template variables. Can be scoped to specific services.

### velocity.dev.yml

Optional local overrides (gitignored). Sets the default instance so you don't need `--instance` every time:

```yaml
instance: my-dev-box
```

## Authentication

```bash
vctl auth login --token strata_pat_xxx
vctl auth status
```

Environment variable overrides:

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
vctl instance provision --name prod-01      # Non-interactive
vctl instance status <name-or-id>           # Refresh status from AWS
vctl instance start <name-or-id>            # Start stopped instance
vctl instance stop <name-or-id>             # Stop running instance
vctl instance terminate <name-or-id>        # Terminate (with confirmation)
vctl instance terminate <name> --force      # Skip confirmation
vctl instance ssh <name-or-id>              # SSH into instance
vctl instance ssh <name> -- -L 8080:localhost:8080
vctl instance use <name-or-id>              # Set default instance
```

### Services

Requires a `velocity.yml` in the current directory.

```bash
vctl service up                    # Full pipeline: sync → runtimes → deps → setup → start → traefik
vctl service up --detach           # Start in background (default: foreground with live output)
vctl service up --skip-sync        # Skip file sync
vctl service up --skip-setup       # Skip runtimes, deps, and setup
vctl service down                  # Stop dev process
vctl service down --all            # Also stop Docker deps and Traefik
vctl service reset                 # Clean slate: remove containers, node_modules, caches
vctl service sync                  # Rsync project files to instance
vctl service status                # Check which service ports are listening
vctl service logs                  # Tail the dev process log
vctl service traefik               # Deploy/update Traefik reverse proxy config
```

#### Provision Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--name` | `-n` | Instance name (required for non-interactive) |
| `--launch-config` | `-l` | Launch config name or ID (uses default if omitted) |
| `--ssh-keys` | `-k` | Comma-separated key names/IDs (auto-generates if omitted) |
| `--auto-keys` | | Force auto SSH key generation |
| `--instance-type` | `-t` | Override instance type (e.g. `m8g.large`) |
| `--domain` | | Subdomain for DNS record |
| `--hosted-zone` | | Route53 hosted zone ID |
| `--no-domain` | | Skip domain provisioning |
| `--no-wait` | | Don't wait for running state |

When run without flags (interactive mode), vctl prompts for each option with sensible defaults.

### SSH Keys

```bash
vctl key list                              # List project SSH keys
vctl key add my-laptop ~/.ssh/id_ed25519.pub
vctl key remove my-laptop
```

Auto SSH key management: when no `--ssh-keys` flag is provided, vctl generates an ed25519 keypair, uploads the public key to Toggle, and cleans up on terminate.

### Launch Configs & Provider

```bash
vctl config list           # List launch configurations
vctl provider status       # Show AWS provider config
vctl provider setup        # Interactive IAM role setup
```

## Service Up Pipeline

`vctl service up` runs these steps in order:

1. **Sync** — rsync project files to the remote instance
2. **Env rewrite** — Rewrite `.env` files for the instance domain
3. **Runtimes** — Check and install declared runtimes (node, go, etc.)
4. **Docker deps** — Start Docker containers (preserves data across restarts)
5. **Setup** — Run `lifecycle.setup` (e.g. `npm install`)
6. **Start** — Run `lifecycle.start` (foreground or detached)
7. **Traefik** — Deploy reverse proxy with SSL (Let's Encrypt) and IP allowlist

## Non-Interactive / Agentic Mode

All commands work without prompts when flags are provided:

```bash
vctl auth login --token $PAT
vctl project use my-project
vctl instance provision --name worker-01 --launch-config "GPU Large" --no-wait
vctl instance ssh worker-01 -- "nvidia-smi"
vctl instance terminate worker-01 --force
```

When stdin is not a TTY, vctl automatically skips prompts, uses defaults, auto-generates SSH keys, and disables spinner animation.

## Config

```
~/.vctl/
├── config.yml          # Endpoint, active project, default instance
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
| `--quiet` | `-q` | Suppress detailed output (verbose by default) |
