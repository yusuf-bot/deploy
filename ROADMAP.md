# Deploy — Complete Roadmap

> **Go-based, single-binary, agent-first personal PaaS. Docker runtime, Caddy reverse proxy, SQLite state, Unix socket API.**
>
> Build: `go build -o bin/deploy .` | Init: `deploy init` | Daemon: `deploy daemon`

---

## Table of Contents

1. [Current State (Phases 1–3)](#current-state-phases-1-3)
2. [Post-Jester Fixes (all shipped)](#post-jester-fixes-all-shipped)
3. [CLI Commands](#cli-commands)
4. [Repository Structure](#repository-structure)
5. [Dependencies](#dependencies)
6. [Features Roadmap](#features-roadmap)
7. [Architecture Decisions](#architecture-decisions)
8. [Open Questions](#open-questions)
9. [Test Strategy](#test-strategy)
10. [Glossary](#glossary)

---

## Current State (Phases 1-3)

### Phase 1 — Daemon Foundation

| Component | Detail |
|-----------|--------|
| **Unix Socket API** | Go 1.22+ `http.ServeMux` with method-based routing (`GET /api/v1/...`, `POST /api/v1/...`). Socket at `/var/run/deploy.sock` (root) or `$XDG_RUNTIME_DIR/deploy/deploy.sock` (non-root). Permissions `0700`. |
| **SQLite** | `modernc.org/sqlite` (pure Go, no CGO). Tables: `apps` + `jobs`. WAL journal mode, `busy_timeout = 5000ms`, `foreign_keys = ON`. |
| **Docker SDK Runner** | `github.com/moby/moby/client`. Container lifecycle: create, start, stop, remove. Health checks via Docker state + HTTP GET. Labels: `deploy.managed`, `deploy.app.id`, `deploy.app.name`. |
| **Env Vars** | `--env KEY=VAL` (repeatable). Serialized as JSON in DB `apps.env` column. |
| **In-Memory Job Scheduler** | Channel-based goroutine pool (5 workers). Jobs have 5-minute timeout. Results persisted to `jobs` table after completion, cleaned from memory after 30s. |
| **SSE Log Streaming** | `text/event-stream` for `deploy logs --follow`. Docker container logs piped via `io.ReadCloser` to SSE writer. |
| **CLI Commands** | `init`, `app create/ls/rm/info/start/stop/logs` |
| **Tests** | ~22 test functions |

### Phase 2 — Build Pipeline + Deploy

| Component | Detail |
|-----------|--------|
| **Build Pipeline** | `internal/build/builder.go` — Docker image build via SDK, git-based versioning (`v<timestamp>-<short-commit>`), tarball save/load for rollback, stack detection (dockerfile, docker-compose, go, node, static, unknown), atomic writes. |
| **`deploy promote`** | Atomic: build → create container → health check → stop old → update app record → activate deployment. Rollback to old container if new one fails health check. |
| **`deploy rollback`** | Load tarball → run same promote flow. Automatically finds previous active deployment if no version specified. |
| **`deploy status`** | Per-app and global. Shows active deployment, recent deployments, deploy-in-progress flag. |
| **Secrets Management** | AES-256-GCM encryption with random nonce. Master key at `~/.deploy/master.key` (permissions `0600`, 32 bytes). Secrets stored encrypted in DB, decrypted on read. Values masked on list. |
| **`deploy.yml` Parser** | Strict YAML with `KnownFields(true)` — unknown fields cause hard errors. Validates app name (`^[a-z][a-z0-9-]*$`), ports (1–65535), durations. |
| **SQLite Tables** | `deployments` (version tracking, container IDs, status, deploy log) + `secrets` (app_id, key, encrypted value) |
| **Migration Runner** | v1 → v2 → v3. Idempotent — safe on every daemon start. `schema_migrations` tracking table. |
| **Single Mutex Lock** | `LockManager` — in-process `map[string]bool` with `sync.Mutex`. Per-app lock prevents concurrent deploys. No three-layer overengineering. |
| **Tests** | ~80 test functions (state + deploy + build + config + scheduler) |

### Phase 3 — Caddy Reverse Proxy

| Component | Detail |
|-----------|--------|
| **Caddy as Subprocess** | NOT embedded. Caddy runs as child process managed by daemon. Binary found via PATH → `~/.deploy/caddy/caddy` (downloaded by `deploy init`). |
| **Caddyfile Manager** | Writes `~/.deploy/caddy/Caddyfile` (main config) + `sites/*.conf` (per-domain snippets). Main Caddyfile uses `import sites/*.conf` glob. |
| **`deploy domain add/rm/ls`** | Add domain → write site snippet → SIGHUP reload. Remove → delete snippet → SIGHUP. List per-app or all. |
| **Auto-SSL** | Let's Encrypt for public domains via Caddy's automatic HTTPS. `tls internal` for `*.localhost` domains. |
| **SIGHUP Reload** | On domain add/rm, promote, rollback. Caddy's graceful reload via `SIGHUP`. |
| **Crash Detection** | Goroutine watches `cmd.Wait()`. Exponential backoff restart (base 1s, max 30s, max 5 attempts). `stopCh` prevents restart loop during intentional shutdown. |
| **Startup Race Fix** | `WaitForReady(timeout)` — polls `IsRunning()` every 200ms before accepting deploys. |
| **`deploy init` Caddy Setup** | Creates caddy dir, detects Caddy on PATH, offers to download v2.8.4 for current platform. |
| **Binary Search Order** | Absolute path → PATH lookup → `~/.deploy/caddy/caddy`. |
| **SQLite Table** | `domains` (id, app_id FK, domain UNIQUE, timestamps). Cascade delete on app removal. |
| **Tests** | ~41 test functions (domains + caddyfile manager + state domains + promote/rollback integration) |

---

## Post-Jester Fixes (all shipped)

> These were shipped after Jester's review — fixes that hardened the codebase:

- **AES-256-GCM secrets encryption** — master key at `~/.deploy/master.key` (`0600` permissions, 32 bytes).
- **Caddy crash detection + restart with backoff** — watcher goroutine, exponential backoff (1s base, 30s cap, 5 attempts).
- **`deploy logs <app>` for running containers** — evolved from `app logs` subcommand to top-level `deploy logs`.
- **HTTP health checks** — not just Docker "running" state but actual HTTP GET (`localhost:<port>/<path>`, 2xx/3xx).
- **Promote rolls back old container** if new one fails health check.
- **Old Bash files archived** to `archive/bash-paas/`.
- **`tls internal` for localhost domains** — no cert errors for dev domains.

---

## CLI Commands

### Current CLI (after Phase 3 + fixes)

| Command | Description | Source |
|---------|-------------|--------|
| `deploy init` | Init environment (dir, DB, master key, caddy, systemd) | `cmd/init.go` |
| `deploy daemon` | Start daemon (hidden) | `cmd/daemon.go` |
| `deploy app create --image --port --env <name>` | Register app | `cmd/app/create.go` |
| `deploy app ls [--status]` | List apps | `cmd/app/ls.go` |
| `deploy app info <name>` | Show app details | `cmd/app/info.go` |
| `deploy app rm <name>` | Delete app (must be stopped) | `cmd/app/rm.go` |
| `deploy app start <name>` | Start app | `cmd/app/start.go` |
| `deploy app stop <name>` | Stop app | `cmd/app/stop.go` |
| `deploy app logs <name> [--tail --follow]` | App container logs (subcommand) | `cmd/app/logs.go` |
| `deploy logs <name> [--tail --follow]` | App container logs (top-level) | `cmd/logs.go` |
| `deploy promote [app-name] [--dir]` | Build + deploy new version | `cmd/promote.go` |
| `deploy rollback <name> [version]` | Rollback to previous version | `cmd/rollback.go` |
| `deploy status [app-name]` | Show deployment status | `cmd/status.go` |
| `deploy secrets set <name> KEY=VAL` | Set secret | `cmd/secrets.go` |
| `deploy secrets get <name> KEY [--raw]` | Get secret | `cmd/secrets.go` |
| `deploy secrets rm <name> KEY` | Remove secret | `cmd/secrets.go` |
| `deploy secrets ls <name>` | List secrets (masked) | `cmd/secrets.go` |
| `deploy images ls [app-name]` | List tarballs | `cmd/images.go` |
| `deploy images rm <name> <version>` | Remove tarball | `cmd/images.go` |
| `deploy domain add <name> <domain>` | Add domain | `cmd/domain.go` |
| `deploy domain rm <name> <domain>` | Remove domain | `cmd/domain.go` |
| `deploy domain ls [app-name]` | List domains | `cmd/domain.go` |

### Target CLI — Planned Evolution

| Keep | Change | Remove / Hide | Add (Phases 4–8) |
|------|--------|---------------|-------------------|
| `init` | `app create` → stay but demote in help | `app logs` (replaced by top-level `logs`) | `audit` |
| `daemon` (hidden) | `app ls` → demote | `images` subcommands → demote from main help | `init --stack <name>` |
| `promote` | `app info` → demote | | `rm <app>` (clean teardown) |
| `rollback` | `app start` → promote to `deploy start` | | `config` (daemon settings) |
| `status` | `app stop` → promote to `deploy stop` | | `ssh <app>` |
| `logs` | `app rm` → stay | | `restart` (promoted from app) |
| `secrets *` | | | `watch` (dev loop) |
| `domain *` | | | `usage <app>` |
| | | | `backup` / `restore` |
| | | | `dev start/stop/logs/url` |
| | | | `dns configure` |

---

## Repository Structure

```
deploy/
├── main.go                    # Entry point — calls cmd.Execute()
├── go.mod / go.sum            # Module: deploy, Go 1.26.2
├── Makefile                   # build, test, install, run-daemon
├── README.md                  # Old Bash-based README (needs rewrite for Go)
├── ROADMAP.md                 # ← You are here
├── .gitignore
├── LICENSE                    # MIT
│
├── cmd/                       # CLI commands (cobra)
│   ├── root.go                # Root cobra command, persistent flags (--json, --wait, --async)
│   ├── daemon.go              # `deploy daemon` — starts all subsystems
│   ├── init.go                # `deploy init` — env setup, DB, caddy download, systemd
│   ├── promote.go             # `deploy promote`
│   ├── rollback.go            # `deploy rollback`
│   ├── status.go              # `deploy status`
│   ├── logs.go                # `deploy logs` (top-level)
│   ├── secrets.go             # `deploy secrets *`
│   ├── domain.go              # `deploy domain *`
│   ├── images.go              # `deploy images *`
│   ├── helpers.go             # runCommand helper
│   ├── promote_test.go        # Promote command test
│   ├── rollback_test.go       # Rollback command test
│   ├── secrets_test.go        # Secrets command test
│   │
│   └── app/                   # `deploy app *` subcommands
│       ├── app.go             # Parent command
│       ├── create.go          # `deploy app create`
│       ├── ls.go              # `deploy app ls`
│       ├── info.go            # `deploy app info`
│       ├── rm.go              # `deploy app rm`
│       ├── start.go           # `deploy app start`
│       ├── stop.go            # `deploy app stop`
│       └── logs.go            # `deploy app logs`
│
├── internal/
│   ├── api/                   # HTTP API server (Unix socket)
│   │   ├── server.go          # Server struct, route registration, ListenAndServe
│   │   ├── handlers.go        # All HTTP handlers (~970 lines)
│   │   ├── middleware.go       # Logging + panic recovery middleware
│   │   ├── errors.go          # Error response helpers
│   │   └── api_test.go        # API tests
│   │
│   ├── build/                 # Build pipeline
│   │   ├── builder.go         # Docker build, context creation, tarball save/load
│   │   ├── builder_test.go
│   │   ├── tarball.go         # Tarball paths, save, load, list, remove, cleanup
│   │   ├── tarball_test.go
│   │   ├── version.go         # Git-based version tagging
│   │   └── version_test.go
│   │
│   ├── caddyfile/             # Caddy reverse proxy management
│   │   ├── manager.go         # Caddy subprocess lifecycle, snippets, reload, crash recovery
│   │   ├── config.go          # Main Caddyfile template, site block builder
│   │   ├── manager_test.go    # Caddy manager tests
│   │   └── (config_test.go inlined in config.go)
│   │
│   ├── client/                # Unix socket HTTP client
│   │   └── client.go          # All API methods (create app → list domains)
│   │
│   ├── config/                # Configuration, paths, validation
│   │   ├── config.go          # HomeDir, SocketPath, DBPath, DeployDir, validation
│   │   ├── deploy_yml.go      # deploy.yml parser, stack detection
│   │   └── deploy_yml_test.go # Config + stack detection tests
│   │
│   ├── deploy/                # Deployment orchestration
│   │   ├── promote.go         # Promote logic: build → check → health → cutover
│   │   ├── promote_test.go
│   │   ├── rollback.go        # Rollback logic: load tarball → promote flow
│   │   ├── rollback_test.go
│   │   ├── status.go          # Per-app + global deployment status
│   │   ├── status_test.go
│   │   ├── lock.go            # Per-app mutex lock manager
│   │   └── lock_test.go
│   │
│   ├── runner/                # Docker SDK abstraction
│   │   ├── interface.go       # Interface + ContainerState + ContainerInfo
│   │   ├── runner.go          # DockerRunner implementation
│   │   └── mock.go            # Mock runner for tests
│   │
│   ├── scheduler/             # Async job scheduler
│   │   ├── scheduler.go       # Channel-based goroutine pool (5 workers)
│   │   └── scheduler_test.go
│   │
│   ├── state/                 # SQLite persistence layer
│   │   ├── db.go              # OpenDB (WAL, busy_timeout, foreign_keys)
│   │   ├── migrations.go      # Migration runner (v1 → v2 → v3)
│   │   ├── apps.go            # App CRUD operations
│   │   ├── jobs.go            # Job persistence
│   │   ├── deployments.go     # Deployment CRUD
│   │   ├── deployments_test.go
│   │   ├── secrets.go         # Encrypted secret CRUD
│   │   ├── secrets_test.go
│   │   ├── domains.go         # Domain CRUD
│   │   ├── domains_test.go
│   │   ├── crypto.go          # AES-256-GCM encrypt/decrypt, master key management
│   │   └── state_test.go      # App state tests
│   │
│   └── types/                 # Shared types, constants, error codes
│       └── types.go           # App, Job, Deployment, Secret, Domain, API request/response types
│
├── archive/                   # Archived Bash prototype
│   ├── README.md
│   └── bash-paas/             # Old Bash scripts (NOT maintained)
│       ├── install.sh
│       └── lib/
│
└── examples/                  # Old Bash-era examples
    ├── deploy.conf.example
    ├── deploy.yml.example
    ├── nginx-custom/
    └── registry.example
```

---

## Dependencies

| Dependency | Version | Purpose | Alternative Considered |
|------------|---------|---------|----------------------|
| `github.com/spf13/cobra` | v1.8.1 | CLI framework | Standard `flag` (too limited for subcommands) |
| `github.com/spf13/pflag` | v1.0.5 | Flag parsing (cobra dep) | — |
| `github.com/google/uuid` | v1.6.0 | UUID generation | `crypto/rand` (UUIDv4 via raw bytes — but less readable) |
| `github.com/moby/moby/api` | v1.55.0 | Docker API types | — |
| `github.com/moby/moby/client` | v0.5.0 | Docker SDK client | Docker CLI exec (too slow, no streaming) |
| `modernc.org/sqlite` | v1.33.1 | Pure Go SQLite (no CGO) | `mattn/go-sqlite3` (requires CGO) |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML parsing | `goccy/go-yaml` (heavier) |
| — | — | — | — |
| **Runtime** | | | |
| Docker | 24+ | Container runtime | Podman (not as widely available) |
| Caddy | v2.8.4 | Reverse proxy + TLS | nginx (too complex to manage as subprocess), Traefik (heavy) |

**Dependency principles:**
- No web framework (stdlib `net/http` is enough for a Unix socket API)
- No ORM (raw SQL with `database/sql` — simpler, more transparent)
- No config framework (custom `deploy.yml` parser with strict validation)
- No logging framework (stdlib `log` package)

---

## Features Roadmap

### Phase 4 — Foundation Ergonomics

| Feature | Description | Status |
|---------|-------------|--------|
| **`deploy audit`** | Evolve from `deploy status`. Add who performed action, duration, health result, deploy log. Structured event log in DB. | 🔜 Planned |
| **`deploy init --stack <name>`** | Project scaffolding: generate Dockerfile, deploy.yml, health check endpoint for common stacks (Go, Node, Python/flask, static). | 🔜 Planned |
| **Slim CLI** | Demote `app create/ls/info/logs` from main help (keep functional). Move `images` under a less prominent spot. Primary commands: `promote`, `rollback`, `status`, `logs`, `secrets`, `domain`. | 🔜 Planned |
| **Auto-cleanup on promote** | Remove old tarballs beyond last 5 (partial — already in `build.Builder.Build()` but needs to also run on `deploy.Deployer.Promote()`). | 🔜 Planned |
| **`deploy rm <app>`** | Clean teardown: stop container, remove tarballs, delete domains + secrets, remove DB records. | 🔜 Planned |
| **`deploy config`** | Manage daemon settings: view, set, edit. Persistent config at `~/.deploy/deploy.yaml`. | 🔜 Planned |

### Phase 5 — Team + Debug

| Feature | Description |
|---------|-------------|
| **Team access** | Unix socket group permissions: `chgrp deploy`, `chmod 770`. Socket-level access control without auth daemon. |
| **`deploy ssh <app>`** | Shell into running container (Docker exec). |
| **`deploy stop` / `deploy restart`** | Promoted from `app stop` / `app start --restart` to top-level commands. |
| **`deploy watch`** | Volume-mount dev loop. Auto-deploy on file change without rebuild. Uses inotify or similar. |

### Phase 6 — Operations

| Feature | Description |
|---------|-------------|
| **`deploy usage <app>`** | Resource trends — CPU, memory, disk over time. Docker stats aggregation. |
| **`deploy backup` / `deploy restore`** | Full system backup: SQLite + tarballs + Caddy config + master key. Atomic restore. |
| **CI/CD docs** | GitHub Actions, GitLab CI examples for `deploy promote` via SSH. |

### Phase 7 — Dev Mode + Git Push

| Feature | Description |
|---------|-------------|
| **Dev mode** | Each prod app can have a dev app (separate port, separate config). Served via tunnel (Cloudflare Tunnel or similar), NOT publicly exposed. Hot reload (volume mounts, file watching). Commands: `deploy dev start/stop/logs/url <app>`, `deploy dev:init` (scaffold with templates), `deploy.yml` `dev` section for custom dev commands. |
| **Git push deploy** | `deploy init` sets up git remote on VPS. `git push deploy main` triggers build + promote via `post-receive` hook. Heroku-style workflow. |

### Phase 8 — DNS Automation

| Feature | Description |
|---------|-------------|
| **`deploy dns configure`** | Set up DNS provider API token (Cloudflare). |
| **Auto-DNS on domain add** | If API configured, auto-create DNS record on `deploy domain add`. |
| **Manual DNS instructions** | If no API configured, print manual DNS setup instructions. |
| **Multiple providers** | Cloudflare as primary example. Extensible provider interface. |
| **Dev subdomain automation** | `<app>.dev.example.com` auto-created. |

### v2 (Deferred — Not Now)

| Feature | Rationale |
|---------|-----------|
| **Preview deployments** (per-branch/per-PR, auto-cleanup) | Useful but complex state management. Wait for demand. |
| **MCP integration** (Model Context Protocol for AI agents) | Interesting but niche. Wait for AI ops patterns to stabilize. |
| **Web UI** | CLI-first is the moat. Web UI would dilute the simplicity. Probably never. |
| **Multi-node orchestration** | Premature. Single-server is the stated design. If needed later, design as separate orchestrator layer. |

---

## Architecture Decisions

### 1. Unix Socket API (not HTTP/TCP)

**Decision:** API listens on Unix socket (`/var/run/deploy.sock`), not TCP.

**Rationale:**
- No network attack surface — only local processes can connect.
- File permissions for access control (`0700` now, `0770` with group later).
- No port conflicts.

**Trade-off:** Cannot serve remote requests (which is intentional — this is a personal PaaS).

### 2. SQLite (not Postgres)

**Decision:** SQLite via `modernc.org/sqlite` (pure Go, no CGO).

**Rationale:**
- Single-server — no need for network database.
- No CGO cross-compilation complexity.
- WAL mode gives adequate concurrency for personal use.
- `busy_timeout` handles the (rare) write contention.

**Limitation:** Not suitable for multi-node or high-concurrency deployments. By design.

### 3. Caddy as Subprocess (not embedded, not standalone)

**Decision:** Caddy runs as a managed subprocess of the deploy daemon.

**Rationale:**
- Automatic TLS via Let's Encrypt (Caddy's core feature).
- Auto-configuration: daemon writes Caddyfile + site snippets.
- Crash detection + restart gives reliability without supervision.
- SIGHUP reload is instantaneous and graceful.

**Trade-off:** Caddy must be installed separately (or downloaded via `deploy init`). Adds ~20MB binary dependency.

### 4. Single Mutex Lock (not distributed, not DB-based)

**Decision:** `LockManager` with in-process `map[string]bool` + `sync.Mutex`.

**Rationale:**
- Single daemon process accesses each app. No distributed locking needed.
- DB-level locks would add complexity for no benefit.
- The three-layer approach (in-memory + DB + file) was overengineering.

**Limitation:** If daemon crashes during deploy, lock is released (by design — no stale lock problem).

### 5. AES-256-GCM for Secrets (not Vault, not env files)

**Decision:** Secrets encrypted with AES-256-GCM using a master key stored at `~/.deploy/master.key` (`0600`).

**Rationale:**
- No external service dependency (Vault, KMS).
- Master key is local — backup strategy must include it.
- GCM provides authenticated encryption (can't tamper with ciphertext).
- Every encryption uses a random nonce (unique per secret write).

**Trade-off:** Master key on disk is vulnerable if attacker has root. Acceptable for personal PaaS. For team use, document HSM/KMS integration path.

### 6. Deployment Tarballs (not registry push)

**Decision:** After Docker build, image is saved as tarball in `~/.deploy/tarballs/<app>/<version>.tar`.

**Rationale:**
- Simple rollback: load tarball → create container.
- No registry needed (no Docker Hub, no ECR/GCR).
- Tarballs are just files — easy to backup, inspect, delete.

**Trade-off:** Tarballs take disk space. Daemon auto-cleans older than last 5.

### 7. Push-Based Deploy (not pull-based)

**Decision:** `deploy promote` runs on the server (or via SSH), builds and deploys locally.

**Rationale:**
- Simplest model — single binary, no webhooks, no CI pipeline.
- Works with `git push deploy main` (Phase 7) via post-receive hook calling `deploy promote`.

**Trade-off:** No GitOps-style reconciliation. No drift detection.

### 8. Port-Per-Deploy (not port-per-app)

**Decision:** Each new deployment gets `app.Port + 1`. After successful health check, old container is stopped and port assignment becomes permanent.

**Rationale:**
- Zero-downtime deploys: new container starts on different port, health check passes, then old container stops.
- Port at `+1` from app's declared port avoids true dynamic allocation.
- Caddy auto-updates site snippets via `UpdatePortSnippets`.

### 9. No Startup Reconciliation

**Decision:** On daemon start, all "running" apps are set to "unknown" status.

**Rationale:**
- Containers may have been stopped/removed while daemon was down.
- "Running" status would be a lie.
- Simpler than trying to reconcile container state vs DB state.
- User can `deploy start` to re-establish.

**Trade-off:** User must manually restart apps after daemon restart. Could add auto-start in Phase 4.

### 10. Cobra CLI (not stdlib flag)

**Decision:** Cobra for CLI framework.

**Rationale:**
- Subcommands (`app create`, `secrets set`, `domain add`) would be painful with stdlib `flag`.
- Help text generation, tabwriter, persistent flags (--json).
- Ubiquitous in Go ecosystem.

### 11. Strict deploy.yml Validation (not lenient)

**Decision:** `yaml.Decoder` with `KnownFields(true)` — unknown fields = hard error.

**Rationale:**
- Catches typos early (e.g., `envrionment` instead of `env`).
- Breaks CI/CD if config is wrong — fails fast.
- Backward compatible: adding new fields is a minor version bump.

### 12. Separate `state`, `deploy`, `api`, `cmd` Layers

**Decision:** Four-layer architecture: `state` (SQLite) → `deploy` (business logic) → `api` (HTTP handlers) → `cmd` (CLI).

**Rationale:**
- Testability: each layer can be tested independently.
- `state` is pure SQL. `deploy` orchestrates `state` + `runner` + `build`. `api` wires HTTP to `deploy`. `cmd` wires Cobra to `client`.
- No circular dependencies.

### 13. No PostgreSQL — SQLite + Token Auth

**Decision:** Stick with SQLite. No PostgreSQL adapter. No DB-level RBAC.

**Auth model:**
- Humans: Unix socket group permissions (chgrp deploy /var/run/deploy.sock, chmod 770)
- CI/CD: API tokens stored in ~/.deploy/users.toml with per-app scope
- Roles: Admin (full access via socket group) + Token-holder (scoped via token)

**Rationale:**
- SQLite handles the scale (50 apps, 5 users, hundreds of deploys) trivially
- Single-binary with zero deps is our competitive moat vs Coolify (Postgres + Redis)
- Unix groups work perfectly for 5-person teams on one VPS
- TOML-based tokens work for CI/CD without adding Postgres
- Adding Postgres now would make us "Coolify but worse" — same complexity, fewer features

**Trade-off:** If we ever outgrow SQLite (unlikely on one VPS), the state layer needs rewriting. That's an acceptable cost at that scale.

**See also:** Open Questions section for full discussion.


---

## Open Questions

### Design

- **Q1:** Should `deploy rm <app>` require confirmation (`--force` to skip)? Currently `app rm` is quiet. ANSWER: Yes, require confirmation. --force flag to skip.
- **Q2:** Should daemon support auto-start of apps on boot (Phase 4)? Or is `deploy start` after daemon start the correct workflow? ANSWER: Yes, default-on with opt-out via deploy config set auto-start=false.
- **Q3:** What's the right approach for multi-architecture builds? Docker buildx? Cross-compilation via QEMU? ANSWER: Deferred. Single-arch for now (detect server platform). Add --platform flag if needed later.
- **Q4:** Should secrets support rotation (key versioning)? Current design is single master key. ANSWER: Yes, should support key versioning. Add in Phase 5.
- **Q5:** Rate limiting on the Unix socket API? Not needed now but could become a concern. ANSWER: v2. Not needed now.
- **Q6:** How to handle container logs rotation? Docker handles this with `--log-opt max-size=max-file`, but deploy doesn't set these. ANSWER: Follow Docker's design. Set sensible defaults (max-size=10m, max-file=3) in the runner. User can override via deploy.yml docker logging options.
- **Q7:** Should `deploy watch` use inotify directly or Docker volume mounts? Docker volumes are simpler but less flexible. ANSWER: No standalone watch feature. Instead `deploy dev start <app>` = volume-mount container with framework hot-reload. ~30 lines. No inotify, no watcher. Phase 5.

### Technical

- **Q8:** `DeployStatusInactive` constant is declared but never used (line 23 in types.go). Remove or implement? ANSWER: Deferred to Ivan for cleanup (see Q8-Q12 action items below).
- **Q9:** `ListSecrets` method is defined in the client interface but the method body is actually `RemoveSecret` (copy-paste bug? Lines 286-291 in client.go). ANSWER: Deferred to Ivan for cleanup (see Q8-Q12 action items below).
- **Q10:** The `handleListDomains` endpoint serves both `/api/v1/apps/{name}/domains` and `/api/v1/domains` — the latter does not have `{name}` but the handler reads `r.PathValue("name")` which will be empty. Need to verify this works correctly. ANSWER: Deferred to Ivan for cleanup (see Q8-Q12 action items below).
- **Q11:** `state.DeactivateOtherDeployments` is called in promote/rollback but has a typo in the name ("Deactivate" vs "Deactivate"). Also, is this the same as `SetDeploymentsInactive` pattern? ANSWER: Deferred to Ivan for cleanup (see Q8-Q12 action items below).
- **Q12:** The `builder.go` `Build()` function performs auto-cleanup of old tarballs, but the `deploy/promote.go` `Promote()` function does its own build via `buildImage()` (a local method) that does NOT run the cleanup. This means promote from the Deployer doesn't auto-clean. Should Deployer.Promote call the Builder? ANSWER: Deferred to Ivan for cleanup (see Q8-Q12 action items below).

### Product

- **Q13:** README.md still describes the old Bash-based deploy with nginx, not the Go version with Caddy. Needs rewrite. ANSWER: Deferred to later. Not now.
- **Q14:** What should the version be? Currently `0.1.0`. Semver suggests 1.0.0 once features stabilize. ANSWER: 0.1.0 is fine for now.
- **Q15:** macOS support? Current socket path logic assumes Linux + `/var/run`. Need to test on macOS. ANSWER: Phase 6-7. Not now.
- **Q16:** Should there be a `deploy upgrade` command that handles binary + migration? ANSWER: v2. Not now.

### Q8-Q12: Technical Cleanup (delegated to Ivan)

- **Q8:** `DeployStatusInactive` in types.go line 23 — remove unused constant or implement it if there's a use case for "inactive" status
- **Q9:** `ListSecrets` in client.go lines 286-291 — copy-paste bug, body calls `RemoveSecret` instead. Fix to actually list secrets.
- **Q10:** `handleListDomains` serves both `/apps/{name}/domains` and `/domains` — verify the path without `{name}` works (r.PathValue("name") will be empty). Either fix the handler or add a separate route.
- **Q11:** `DeactivateOtherDeployments` typo + is it redundant with `SetDeploymentsInactive`? Audit both paths and consolidate.
- **Q12:** `builder.go.Build()` has auto-cleanup but `deploy/promote.go.Promote()` calls `buildImage()` which does NOT cleanup. Should Deployer.Promote call Builder? Fix so promotes auto-cleanup tarballs.

---

## Test Strategy

### Current Coverage (~177 test functions across 18 test files)

| Package | Test File | Functions | What's Tested |
|---------|-----------|-----------|---------------|
| `cmd` | `promote_test.go` | 1 | Promote command integration (mock server) |
| `cmd` | `rollback_test.go` | 1 | Rollback command integration |
| `cmd` | `secrets_test.go` | 1 | Secrets command integration |
| `internal/api` | `api_test.go` | ~12 | HTTP handlers: create/list/get/delete app, start/stop/logs, promote/rollback, status, secrets, domains |
| `internal/build` | `builder_test.go` | ~3 | Build context creation, tar operations |
| `internal/build` | `tarball_test.go` | ~5 | Tarball path, save, load, list, remove, cleanup |
| `internal/build` | `version_test.go` | ~3 | Git version tagging |
| `internal/caddyfile` | `manager_test.go` | ~8 | Caddy manager: start, stop, reload, crash detection, snippets |
| `internal/config` | `deploy_yml_test.go` | ~18 | deploy.yml parsing, validation, stack detection |
| `internal/deploy` | `lock_test.go` | ~3 | Lock acquire, release, double-acquire rejection |
| `internal/deploy` | `promote_test.go` | ~5 | Promote with mock runner: success, health check failure rollback |
| `internal/deploy` | `rollback_test.go` | ~3 | Rollback with mock: success, missing tarball |
| `internal/deploy` | `status_test.go` | ~3 | Per-app and global status |
| `internal/scheduler` | `scheduler_test.go` | ~4 | Job scheduling, execution, persistence, timeout |
| `internal/state` | `state_test.go` | ~15 | App CRUD, env serialization, status updates |
| `internal/state` | `deployments_test.go` | ~8 | Deployment CRUD, active deployment, status transitions |
| `internal/state` | `domains_test.go` | ~14 | Domain CRUD, uniqueness, cascade delete |
| `internal/state` | `secrets_test.go` | ~14 | Secret CRUD, encrypt/decrypt, master key management |

### What To Add

| Priority | Area | What's Missing |
|----------|------|----------------|
| **High** | `deploy/status.go` | No unit test for `Status()` and `GlobalStatus()` methods (only the API handler tests exist) |
| **High** | `state/apps.go` | Edge cases: update non-existent app, delete app with running status, concurrent status updates |
| **Medium** | `runner/runner.go` | No DockerRunner-specific tests (uses mock in other tests). Docker-specific edge cases: network errors, container already exists |
| **Medium** | `state/crypto.go` | Edge cases: corrupt ciphertext, wrong key size, empty plaintext, large plaintext (>1MB) |
| **Medium** | `api/handlers.go` | SSE streaming edge cases: client disconnect, slow consumer, large log output |
| **Medium** | `cmd/app/info.go` | Info command has no tests |
| **Low** | `build/tarball.go` | Concurrent tarball operations, cleanup edge cases |
| **Low** | `scheduler/scheduler.go` | Full queue scenario, worker pool exhaustion |
| **Low** | `caddyfile/manager.go` | SIGHUP behavior, orphaned snippet cleanup, concurrent snippet writes |
| **Low** | Integration tests | End-to-end: deploy init → daemon start → app create → promote → domain add → verify HTTPS |

### Test Infrastructure

```go
// Mock runner is in internal/runner/mock.go — used by deploy and api tests.
// SQLite tests use :memory: database with the migration runner.
// Caddy manager tests use a fake caddy binary (shell script that sleeps).
```

**Running tests:**
```bash
make test                # Full test suite with verbose
make test-short          # Without verbose (for CI)
go test ./... -count=1   # Without cache
go test ./internal/state/... -run TestEncrypt -v  # Single test
```

---

## Glossary

| Term | Definition |
|------|------------|
| **App** | A deployed application record in SQLite. Has a name, port, Docker image, env vars, and status. |
| **Deployment** | A specific version of an app that was deployed. Has a version string, container IDs, status, and deploy log. |
| **Promote** | Build + deploy + health check + cutover. The primary deploy operation. |
| **Rollback** | Load previous tarball → deploy as new version → health check → cutover. |
| **Secret** | An encrypted environment variable stored in DB. AES-256-GCM encrypted with master key. |
| **Domain** | A custom domain (e.g., `app.example.com`) attached to an app. Managed via Caddy snippets. |
| **Tarball** | A `.tar` file containing a saved Docker image. Used for rollback. Stored in `~/.deploy/tarballs/`. |
| **Master Key** | 32-byte AES-256 key at `~/.deploy/master.key`. Used to encrypt/decrypt secrets. |
| **Caddy Manager** | Manages Caddy subprocess lifecycle, Caddyfile config, and SIGHUP reloads. |
| **Lock Manager** | In-process per-app mutex to prevent concurrent deploys. |

---

## Design Principles

1. **Single binary, zero runtime deps beyond Docker + Caddy.** `deploy` is the daemon and CLI in one binary.
2. **Unix socket for security.** No network ports for the API.
3. **SQLite for simplicity.** No Postgres, no Redis. Just a file. (See Architecture Decision #13 for full auth model.)
4. **Caddy for TLS + reverse proxy.** Auto-SSL via Let's Encrypt. Zero config for TLS.
5. **Tarballs for rollback.** No registry. Just files.
6. **Push-based deploys.** `deploy promote` on the server (or via SSH). No CI/CD pipeline required.
7. **CLI-first.** No Web UI. The terminal is the interface.
8. **Fail fast.** Strict YAML validation, health checks on every deploy, rollback on failure.
9. **No overengineering.** Single mutex lock. No distributed consensus. No service mesh.
10. **Agent-friendly.** Unix socket API is plain HTTP/JSON. Easy for AI agents to interact with.

---

> **Last updated:** 2026-07-26
> **Version:** 0.1.0
> **Maintainer:** [OpenCode AI] — coordinated by Oscar, planned by Scout, implemented by Ivan, reviewed by Jester.
