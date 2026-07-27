# deploy — single-binary personal PaaS

Deploy is a single-binary, agent-first personal PaaS for managing Docker containers on a single server. It provides a persistent daemon with a Unix socket API, Docker-based deployment pipeline, Caddy reverse proxy with automatic SSL, and SQLite-backed state management.

Key features:
- Single 21MB Go binary, no CGO, no PostgreSQL, no Redis
- Docker build + deploy pipeline with health checks and rollbacks
- Caddy reverse proxy with automatic Let's Encrypt SSL
- Git push deploy workflow
- Development containers with volume mounts
- Encrypted secrets (AES-256-GCM)
- DNS record management (6 providers)
- Audit logging and backup/restore

## Quick Start

```bash
# Install (one of these):
curl -fsSL https://deploy.sh | sh

# Initialize the environment
deploy init

# Deploy your first app
cd my-project
deploy promote myapp
```

## Install

### Option 1: Install script

```bash
curl -fsSL https://deploy.sh | sh
```

Downloads the latest release binary to `/usr/local/bin/deploy`.

### Option 2: Pre-built binary

Download from [GitHub Releases](https://github.com/yusuf-bot/deploy/releases):

```bash
curl -fsSL https://github.com/yusuf-bot/deploy/releases/download/v0.2.0/deploy_v0.2.0_linux_amd64.tar.gz
tar -xzf deploy_v0.2.0_linux_amd64.tar.gz
sudo cp deploy /usr/local/bin/deploy
```

### Option 3: Build from source

```bash
git clone https://github.com/yusuf-bot/deploy.git
cd deploy
go build -o bin/deploy .
sudo cp bin/deploy /usr/local/bin/deploy
```

Requires Go 1.26+. No CGO required.

## Initialize

```bash
deploy init
```

This creates:
- `~/.deploy/` data directory with SQLite database and configuration
- `~/.deploy/master.key` AES-256-GCM master key (0600 permissions)
- `~/.deploy/caddy/` Caddy working directory
- Optionally downloads Caddy v2.8+ for reverse proxy and SSL termination

## Start the daemon

```bash
deploy daemon
```

The daemon runs as a foreground process. For production, use the provided systemd unit:

```
[Unit]
Description=deploy daemon
After=docker.service
Requires=docker.service

[Service]
ExecStart=/usr/local/bin/deploy daemon
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

## Configuration

### deploy.yml

Place a `deploy.yml` in your project root to define build and runtime configuration:

```yaml
build:
  dockerfile: Dockerfile
  context: .
  args:
    NODE_ENV: production

health:
  path: /health
  initial_delay: 2s
  interval: 3s
  timeout: 5s
  retries: 10

ports:
  - container: 8080

domains:
  - example.com

env:
  NODE_ENV: production
  LOG_LEVEL: info

dev:
  command: npm run dev
  port: 3000
  volumes:
    - source: ./src
      target: /app/src
```

All fields are optional. The daemon validates the YAML strictly (unknown fields produce errors).

### Daemon settings

```bash
deploy config set dns_provider cloudflare
deploy config set dns_token <api-token>     # stored encrypted
deploy config get dns_token                 # shows ***
deploy config get dns_token --reveal        # shows plaintext
deploy config get                           # list all settings
```

Sensitive settings (`dns_token`, `dns_secret`) are automatically encrypted with the master key before storage.

## Command Reference

### Deployment

| Command | Description |
|---------|-------------|
| `deploy promote <app>` | Build Docker image, start new container on port+1, health check, stop old container |
| `deploy rollback <app> [version]` | Rollback to a previous deployment version |
| `deploy status [app]` | Show deployment status for all apps or one app |
| `deploy logs <app>` | Stream container logs |
| `deploy stop <app>` | Gracefully stop a container (SIGTERM + 30s grace period) |
| `deploy restart <app>` | Sequential stop then start |
| `deploy rm <app>` | Remove an app with clean teardown (requires confirmation) |

### Domains and DNS

| Command | Description |
|---------|-------------|
| `deploy domain add <app> <domain>` | Attach a domain to an app (updates Caddy config) |
| `deploy domain rm <app> <domain>` | Remove a domain from an app |
| `deploy domain ls [app]` | List domains |
| `deploy domain dns sync <app> --ipv4 <ip> [--ipv6 <ip>]` | Create or update A/AAAA records for all app domains |
| `deploy domain dns list <app>` | List DNS records for app domains |

### Secrets (encrypted)

| Command | Description |
|---------|-------------|
| `deploy secrets set <app> <key>=<value>` | Add or update an encrypted secret |
| `deploy secrets get <app> <key>` | Retrieve a decrypted secret value |
| `deploy secrets rm <app> <key>` | Remove a secret |
| `deploy secrets ls <app>` | List secret keys |

### Development

| Command | Description |
|---------|-------------|
| `deploy dev start <app>` | Start a development container with volume mounts (port = app.port + 1000) |
| `deploy dev stop <app>` | Stop and remove the development container |

### Git push deploy

| Command | Description |
|---------|-------------|
| `deploy git setup <app>` | Create a bare git repository with post-receive hook that auto-deploys on push |

### Operations

| Command | Description |
|---------|-------------|
| `deploy backup` | Create a full system backup (SQLite VACUUM INTO + tar archive) |
| `deploy restore <backup-file>` | Restore from a backup (daemon must be stopped) |
| `deploy audit [app]` | Show deploy audit log entries |
| `deploy config get [key]` | Get daemon settings (secrets masked by default) |
| `deploy config set key=val` | Set a daemon setting |
| `deploy usage <app>` | Show container resource usage (CPU, memory) |
| `deploy ssh <app>` | Open an interactive shell in the running container |

### System

| Command | Description |
|---------|-------------|
| `deploy init` | Initialize the deploy environment |
| `deploy daemon` | Start the deploy daemon |
| `deploy uninstall` | Remove deploy, its data, and systemd service |
| `deploy version` | Print the version |

## DNS Providers

Supported DNS providers for automatic A and AAAA record management:

| Provider | Auth |
|----------|------|
| Cloudflare | API Token (Bearer) |
| DigitalOcean | Personal Access Token |
| Hetzner | Cloud API Token |
| Linode | Personal Access Token |
| Vultr | API Key |
| Porkbun | API Key + Secret Key |

Route53 support is planned for a future release.

Configure the provider via daemon settings:

```bash
deploy config set dns_provider cloudflare
deploy config set dns_token <api-token>
```

Then sync DNS records for an app:

```bash
deploy domain dns sync myapp --ipv4 203.0.113.10
```

## Architecture

```
+-------------------+      Unix Socket       +-------------------+
|  deploy CLI       | <--------------------> |  deploy daemon    |
|  (cobra commands) |      /var/run/         |  (HTTP API)       |
+-------------------+      deploy.sock       +-------------------+
                                                    |
                      +-----------------------------+------------------+
                      |              |              |                  |
                 +---------+   +---------+   +-----------+   +--------------+
                 | SQLite  |   | Docker  |   | Caddy     |   | Tarball      |
                 | State   |   | Runtime |   | Subprocess|   | Storage      |
                 +---------+   +---------+   +-----------+   +--------------+
```

- **CLI** sends commands to the daemon over a Unix socket (0700 permissions)
- **Daemon** manages state in SQLite (WAL mode, busy_timeout 5s), runs Docker containers, and manages a Caddy subprocess for reverse proxy and SSL
- **Promote** flow: build image, save tarball for rollback, start new container on port+1, health check, update Caddy config, stop old container
- **Secrets** are encrypted with AES-256-GCM using a master key stored at `~/.deploy/master.key` (0600)
- **Images** are stored as tarballs in `~/.deploy/images/` (last 5 kept per app)

## Security

- All sensitive values (`dns_token`, `dns_secret`) are encrypted with AES-256-GCM before storage
- Master key stored at `~/.deploy/master.key` with 0600 permissions
- SQLite database at 0600 permissions
- Unix socket at 0700 permissions (accessible only by owner)
- Container stop uses SIGTERM with 30-second grace period before SIGKILL
- Encrypted secrets are decrypted only at runtime when passed as environment variables to containers

## Requirements

- Linux (kernel 4.0+)
- Docker Engine (20.0+)
- Go 1.26+ (only for building from source)

Optional:
- Caddy v2.8+ (auto-downloaded by `deploy init`, for reverse proxy and SSL)

## License

MIT License. Copyright (c) 2026 yusuf-bot.
