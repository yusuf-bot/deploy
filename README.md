# deploy — self-hosted platform for solo devs, startups, and SMEs

![Version](https://img.shields.io/badge/version-0.3.0-10b981)
[![Website](https://img.shields.io/badge/website-deploy.openexplorer.xyz-10b981)](https://deploy.openexplorer.xyz)
[![Docs](https://img.shields.io/badge/docs-docs.deploy.openexplorer.xyz-059669)](https://docs.deploy.openexplorer.xyz)
[![GitHub](https://img.shields.io/badge/github-yusuf--bot/deploy-1a1a2e)](https://github.com/yusuf-bot/deploy)

Deploy is a single-binary, CLI-first platform for deploying and managing applications on your own server. Think Heroku-quality DX without the Heroku bill — one 21MB binary, zero dependencies beyond Docker, and everything you need to go from `curl` to `https://yourapp.com` in under 3 minutes.

## Who is this for?

| Audience | Use case |
|----------|----------|
| **Solo devs** | 1-5 side projects, personal apps, family tools. Low complexity, low cost. |
| **Startups** | 3-15 people, staging + production envs, CI/CD pipeline, preview deploys |
| **SMEs** | 15-100 people, team permissions, audit trails, uptime monitoring |

Right now we're building for **solo devs first**. Everything works for one person on one server. Team features, preview deploys, and RBAC come as we grow.

## Quick Start

```bash
# One command — downloads binary, checks Docker, installs Caddy
curl -fsSL https://deploy.openexplorer.xyz/install.sh | sh

# Interactive setup wizard (prompts for auto-start, systemd)
sudo deploy init

# Scaffold deploy.yml + Dockerfile for your project (auto-detects stack)
cd my-project
deploy scaffold

# Deploy — builds, health checks, zero-downtime swap
deploy up myapp

# Add a domain + automatic SSL
deploy domain add myapp example.com
```

## What makes deploy different

**Everything built-in, nothing to install.** No PostgreSQL, no Redis, no Kubernetes, no plugin hunt. One binary, one daemon, one SQLite file.

**CLI-first, composable.** Works in CI, over SSH, in GitHub Actions, in scripts. `deploy up`, `deploy rollback`, `deploy status` — every command works without a browser.

**Agent-native.** MCP server on the roadmap means AI can manage your deploys directly.

**Zero-downtime deploys.** Build → start new container on separate port → health check → update Caddy → stop old. Old container never killed until new one is healthy.

**SSL in one command.** Attach a domain and Caddy handles Let's Encrypt automatically.

**Encrypted secrets.** AES-256-GCM encrypted at rest, decrypted only at runtime.

## Features

### Core
- `deploy up` — Build Docker image, run health checks, zero-downtime swap
- `deploy rollback` — Revert to any of the last 5 deployed versions
- `deploy start/stop/restart/rm` — Full app lifecycle
- `deploy status` — See all apps and their health
- `deploy logs` — Stream container logs

### Domains
- `deploy domain add/rm/ls` — Attach domains to apps
- Automatic Let's Encrypt SSL via Caddy

### Secrets
- `deploy secrets set/get/rm/ls` — AES-256-GCM encrypted
- Decrypted at runtime, passed as container env vars

### Development
- `deploy dev start/stop` — Dev containers with volume mounts (port + 1000)
- `deploy scaffold` — Auto-detect stack, generate deploy.yml + Dockerfile

### Operations
- `deploy audit` — Full audit log of every action
- `deploy backup / restore` — Full system backup and restore
- `deploy config set/get` — Persistent daemon settings
- `deploy usage` — Container CPU/memory
- `deploy ssh` — Interactive shell into running containers
- `deploy uninstall` — Remove everything cleanly

### CI/CD
- `deploy git setup` — Git post-receive hook auto-deploys on push
- Works with GitHub Actions, GitLab CI, any SSH-able CI
- All commands return structured JSON with `--json`

## Architecture

```
+-------------------+      Unix Socket       +-------------------+
|  deploy CLI       | <--------------------> |  deploy daemon    |
|  (cobra commands) |   /var/run/deploy/     |  (HTTP API)       |
+-------------------+      deploy.sock       +-------------------+
                                                    |
                      +------------------------------+------------------+
                      |              |               |                  |
                 +---------+    +---------+    +-----------+    +--------------+
                 | SQLite  |    | Docker  |    | Caddy     |    | Tarball      |
                 | State   |    | Runtime |    | Subprocess|    | Storage      |
                 +---------+    +---------+    +-----------+    +--------------+
```

- **CLI** → daemon over Unix socket at `/var/run/deploy/deploy.sock` (0770, deploy group)
- **Daemon** manages state in SQLite (WAL mode), runs Docker containers, manages Caddy subprocess
- **Promote flow**: build → save tarball → allocate free port → create container → health check → update Caddy → stop old container
- **Secrets** encrypted with AES-256-GCM, master key at `~/.deploy/master.key` (0600)
- **Images** stored as tarballs in `~/.deploy/images/` (last 5 per app)

## Comparison

| | deploy | Dokku | Coolify | Railway |
|---|---|---|---|---|
| Install size | 21MB binary | apt/pkg 50MB+ | Docker stack ~800MB | N/A (managed) |
| Dependencies | Docker only | Docker | Docker + Postgres + Redis | N/A |
| CLI | Native Go | Shell scripts | Web UI only | Web UI + CLI |
| CI/CD friendly | ✅ | ⚠️ | ❌ | ⚠️ |
| Secrets mgmt | Built-in AES-256 | Plugin | ❌ | Platform |
| Audit logs | Built-in | ❌ | ❌ | ❌ |
| MCP server | Planned | ❌ | ❌ | ❌ |
| Zero-downtime | ✅ | Plugin | ⚠️ | ✅ |
| Price | Free (self-host) | Free | Free (self-host) | Usage-based |

## Requirements

- Linux (kernel 4.0+)
- Docker Engine (20.0+)
- Go 1.26+ (only for building from source)

## Roadmap

See [ROADMAP.md](ROADMAP.md) for the full plan.

✅ **v0.3.0 — Solo dev experience**: released. Critical bugs fixed (permission model, promote flow, tar build context, health check, config UX), DNS zone extraction for 6 providers, interactive init wizard, scaffold command, graceful shutdown.

## Design Decisions

| Decision | Choice |
|----------|--------|
| Socket location | `/var/run/deploy/deploy.sock` (0770, deploy group) |
| Database | SQLite default, optional PostgreSQL for teams |
| Config format | deploy.yml = production, docker-compose.yml = local dev |
| Main CLI command | `deploy up` (not `deploy promote`) |
| Rollback strategy | Tarballs default, tag-based optional |

## License

MIT License. Copyright (c) 2026 yusuf-bot.
