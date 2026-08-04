# Roadmap

Targeting dev agencies/freelancers → startup teams → SMEs, one segment at a time: agencies running 5-40 client apps on one VPS buy capability (per-app restore, isolation, audit) before anything else; startup teams buy only after trust is proven; side-project solo devs are a free distribution channel, not a buyer. v0.2.0 has the bones of a good product but critical bugs prevent even a single app from having a working experience. v0.3.0 fixes every bug in the core loop before adding any features.

## v0.2.0 (Fixed in Phase 1)

The code has the right architecture but every part of the core loop has a bug:

### Bugs found (14+ — all fixed in Phase 1 + Round 2)

**Critical — nothing works for non-root user:**
- Socket at `~/.deploy/deploy.sock` with 0700 permissions. Daemon runs as root. Non-root CLI can't connect.
- `sudo deploy init` creates ALL files as root, never chowns to real user.
- SUDO_USER detection hardcodes `/home/{name}` instead of `os/user.Lookup()`.
- `runDaemon()` doesn't detect SUDO_USER at all.

**Core loop broken:**
- Promote kills old container BEFORE build. If build fails, app stays down. If build takes 5 min, 5 min downtime.
- Tar "write too long" on growing files (logs, DBs) in Docker build context. No auto-exclude patterns.
- Caddy download has no O_TRUNC and no atomic write → "text file busy" error.
- Health check uses `localhost` instead of `127.0.0.1` → IPv6 resolution failure on some systems.

**UX friction:**
- `deploy config set auto_start true` fails — only accepts `key=val` format, not `key val`.
- `deploy config get key` without `--reveal` fetches ALL settings just to return one.
- `Rollback()` doesn't set `StatusRunning` → wrong state after rollback.
- Async jobs never update app status on completion.
- `deploy daemon` appears twice in help (stale binary issue).
- No `deploy start` command in some builds (build pipeline inconsistency).

## v0.3.0 — Solo dev experience

**Goal**: A solo dev goes from `curl` to `https://myapp.com` in under 3 minutes. No broken steps. No permission puzzles. No port conflicts.

### Phase 1: Fix critical bugs (immediate)

| ✅ | # | Bug | Fix | File | Status ✅ |
|---|---|---|---|---|---|
| ✅ | 1 | Socket 0700 blocks non-root | Move socket to `/var/run/deploy/deploy.sock`, 0770, deploy group. Add user to deploy group on init. | `internal/api/server.go:86`, `cmd/init.go` | Done |
| ✅ | 2 | Promote kills old before build | Restore correct flow: build → allocate port → start new → health check → update Caddy → stop old. Never kill before build. | `internal/deploy/promote.go:164-169` | Done |
| ✅ | 3 | sudo init creates in /root | Use `os/user.Lookup(SUDO_USER)` for home. Chown all created files to real user. Add SUDO_USER detection in `runDaemon()`. | `cmd/init.go:33`, `cmd/daemon.go:31` | Done |
| ✅ | 4 | Tar write too long | Auto-exclude patterns: `node_modules/`, `.git/`, `*.log`, `*.db`, `.cache/`, `tmp/`. Re-stat files before tarring. Use `io.CopyN` as safety net. | `internal/build/builder.go:88-111` | Done |
| ✅ | 5 | Caddy download text file busy | Atomic write: download to temp file, rename to final path. Add O_TRUNC. | `cmd/init.go:169` | Done |
| ✅ | 6 | Health check localhost → IPv6 | Change `http://localhost:%d%s` to `http://127.0.0.1:%d%s` | `internal/runner/runner.go:305` | Done |
| ✅ | 7 | Config set syntax | Support both `key=val` (1 arg) and `key val` (2 args). Use `RangeArgs(1,2)`. | `cmd/config.go:89` | Done |
| ✅ | 8 | Config get wasteful fetch | Call `GetConfigKey(key, reveal)` in both branches, not `GetConfig()` for non-reveal. | `cmd/config.go:35` | Done |
| ✅ | 9 | Rollback missing status update | Add `UpdateAppStatus(tx, appName, StatusRunning)` in rollback transaction. | `internal/deploy/rollback.go:216-242` | Done |
| ✅ | 10 | Promote rename to `deploy up` | Create `cmd/up.go` as alias for promote. Eventually make `promote` hidden. | `cmd/up.go` (new) | Done |

### Phase 1: Complete

All 10 Phase 1 bugs fixed in a single pass. Build compiles, vet passes, binary works.
- Socket at `/var/run/deploy/deploy.sock` 0770, daemon runs as root, CLI connects as user
- SUDO_USER detection uses `os/user.Lookup()` not hardcoded path
- Init chowns all files to real user after creation
- Promote no longer kills old container before build
- Tar build context auto-excludes common patterns + uses CopyN
- Caddy download uses atomic write (temp → rename)
- Health check uses `127.0.0.1` instead of `localhost`
- Config set accepts both `key=val` and `key val`
- Config get calls GetConfigKey() not wasteful GetConfig()
- Rollback sets StatusRunning in transaction
- `deploy up` added as primary command (promote alias)

### Phase 1 Round 2: Security & hardening (complete)
16 additional bugs found by Scout audit after initial fixes:
- Data race in scheduler job status (mutex-protected writes)
- Lock key mismatch in global status (app.ID vs app.Name)
- Path traversal in filesystem handlers (validated app names)
- downloadCaddy no timeout (added 120s timeout)
- chownToUser logs chown errors instead of swallowing
- Auto-start logs Docker errors instead of silent skip
- Global status logs DB errors instead of silent drop
- Duplicate deploy.yml load in promote (load once)
- RemoveContainer errors logged on failed start
- JSON marshal errors handled properly in CLI
- Caddy restart counting fixed (removed double-loop)
- Health check result logged in startApp
- Master key overwrite logs warning
- Config set validates empty keys
- Inconsistent --wait handling removed from start/stop
- PID race in Caddy process check (goroutine+channel pattern)
### Phase 2: Core loop hardening (P0-P1)

- [x] **Port reservation pool**: SQLite-backed table tracks allocated ports. Check Docker for actual port usage. No more `app.Port + 1`.
- [x] **State reconciliation on daemon start**: Query Docker for containers with `deploy.managed=true` label. Match against DB state. Don't reset to "unknown".
- [x] **Single build code path**: Delete `Deployer.buildImage()`. Route everything through `Builder.Build()` which handles build args, `.dockerignore`, multi-stage.
- [x] **Caddy template**: Replace `strings.ReplaceAll` for port updates with proper Go template per app. Regenerate entire snippet on port/domain change.

### Phase 3: Install & init (P1)

- [x] **Install script** (`scripts/install.sh`):
  - Detect OS/arch, download correct binary
  - Check Docker — **warn if missing, don't auto-install** (too many distros)
  - Check Caddy — download to `/usr/local/bin/caddy` if missing
  - Run `deploy init` as the real user (detect SUDO_USER)
  - Print next steps
- [x] **Init wizard**:
  - Detect Docker version, Caddy presence
  - ~~Prompt for DNS provider + API token (skippable, all 6 providers)~~ — **REMOVED in `7d4684ec`** (no DNS automation; domains via `deploy domain add`, DNS done manually/Cloudflare once)
  - Prompt for auto-start, systemd setup
  - Show summary before applying
  - Print next steps
  
### Phase 4: Scaffold & UX (P1)

- [x] **`deploy scaffold`**: Auto-detect stack (go.mod, package.json, requirements.txt, etc.). Generate a WORKING multi-stage Dockerfile + deploy.yml that builds and deploys without edits for the detected stack. If Dockerfile already exists, just generate deploy.yml.
- [x] **Kill duplicate commands**: Remove `deploy app start/stop/logs/rm`. Keep flat hierarchy.
- [x] **deploy.yml vs docker-compose.yml docs**: deploy.yml is production config. docker-compose is local dev. deploy never touches docker-compose.yml.

### Phase 5: DNS & testing (P2)

> **DNS automation REMOVED** in `7d4684ec` (v0.3.0): the 6 DNS provider integrations
> (cloudflare, digitalocean, hetzner, linode, porkbun, vultr) and the DNS sync/list
> commands were deleted. The replacement is a deterministic Caddyfile — domains must
> be added via `deploy domain add`, and DNS records are configured manually (once, at
> Cloudflare or the registrar). Struck-through items are obsolete, not done.

- ~~[x] **Zone extraction**: For domains like `blog.example.com`, extract zone (`example.com`) automatically for all 6 DNS providers.~~ — **REMOVED in `7d4684ec`**
- ~~[x] **DNS provider tests**: Mock HTTP tests for all 6 providers covering API errors, timeouts, edge cases.~~ — **REMOVED in `7d4684ec`**
- [x] **Integration tests**: Docker-based tests for promote, rollback, dev container lifecycle.
- [x] **Graceful shutdown**: SIGTERM/SIGINT handling — drain requests, save state, stop containers.
- ~~[x] **HTTP status code checks**: All DNS provider API calls validate HTTP status codes properly.~~ — **REMOVED in `7d4684ec`**

### Phase 6: Future

### Rollback strategy decision

**Tarballs (current approach)**: Every deploy saves a full Docker image tarball (`docker save`). Last 5 kept per app. Survives `docker system prune --all`. Portable across machines (backup/restore). Downside: 300MB-1GB per deploy, slow to load.

**Tags (alternative)**: Tag deployed images as `<app>:<version>`. Keep last 5 tags. Rollback is instant (`docker run <app>:<old-version>`). Downside: `docker system prune -a` removes untagged images.

**Decision**: Keep tarballs, add `deploy config set rollback_strategy=tarball|tag` to let users choose. Default is tarball for safety. Tags option is faster for solo devs who don't prune aggressively.

> **REVISED (v0.3.x audit)** — tarball is the default and **only** option. The tag-based option is **REMOVED from the roadmap** until it is actually implemented (docs honesty rule: claims must match code). See Design Decisions → Rollback strategy.

## Shipped in v0.3.0

These shipped in v0.3.0 / v0.3.x but were never recorded in the roadmap:

- **`network:` field in deploy.yml** — join existing Docker networks (e.g. a shared DB container)
- **`DEPLOY_DATA_DIR` env override** — relocatable data directory (moved to `/mnt/bigvolume` on this host)
- **HTTP-only domains support** — no TLS block emitted when a domain is outside certificate coverage
- **Caddy QUIC patch** — stable reload (QUIC disabled, durable dual-binding with origin cert)
- **zstd tarball compression** — ~67% smaller images; legacy `.tar` archives remain readable
- **`deploy prune` command** — keep N images per app, with `--dry-run`
- **Secrets injected on ALL container-start paths** — previously promote-only
- **promote → deploy messaging cleanup** — `deploy up` is the primary command
- **Documentation site** — live at deploy.openexplorer.xyz with Nextra docs — Getting Started, Guides, Reference, Troubleshooting (moved here from the old v0.4.0 section when it was re-homed)

## v0.3.x — core-loop bugs (resolved)

All five items on the previous "immediate priority" list are resolved — none remain open:

1. ~~**`deploy up` writes the PREVIOUS port into the Caddy site conf**~~ — **FIXED**. Deterministic conf rewrite + restart per app; verified by `internal/integration/caddy_test.go`.
2. ~~**`deploy start` / `deploy restart` broken**~~ — **FIXED**. Start/restart no longer pull `<app>:latest` from the registry; they fall back to the local image. Verified by `internal/api/api_test.go` → `TestStartSkipsRegistryPull`.
3. ~~**Port drift**~~ — **FIXED**. Ports are reused from the SQLite allocation pool, not `app.Port + 1`. Verified by `internal/state/ports_test.go` → `TestAllocatePortReusesExistingAllocation` and `internal/integration/promote_test.go` → `TestPromoteReusesPort`.
4. ~~**Integration tests**~~ — **DONE** in `internal/integration/` (Docker-based promote/rollback/prune/dev lifecycle tests, hermetic `DEPLOY_DATA_DIR`).
5. ~~**`rollback_strategy=tarball|tag`**~~ — **REMOVED**. Decision revised: tarball is the default and only option; the tag path is not implemented. See Design Decisions → Rollback strategy.

## v0.4.0 — Agency ops (shipped)

**Goal**: A dev agency can onboard a client app and prove recovery in minutes on a single VPS — per-app backup/restore, isolation, and audit, with no per-client infrastructure.

| Task | Status | Notes |
|------|--------|-------|
| **Per-app backup/restore** — `deploy backup [app]` / `deploy restore <file> [app]` | ✅ Done | `internal/deploy/restore.go`, `internal/state/backup.go` (ExportApp/ImportApp), `internal/integration/backup_test.go` |
| **Resource limits** — deploy.yml `resources:` validation + persistence to DB | ✅ Done | Migration v8 (`apps.memory`/`apps.cpus`), validation in promote.go |
| **Per-app usage + disk** — `deploy usage` with aggregate + `--json` | ✅ Done | `GET /api/v1/usage`, CLI `deploy usage` |
| **Uptime monitoring + webhook alerts** — health checker every 30s, webhook POST on ok→failed with 5-min cooldown | ✅ Done | Migration v10 (`apps.health_path`, `app_health`), daemon goroutine, `webhook_url`/`webhook_secret` settings |
| **Env groups** — shared env vars per app, `deploy envgroup` CLI, merge order: deploy.yml < group < secrets | ✅ Done | Migration v9 (`env_groups`, `env_group_vars`, `apps.group_id`), `cmd/envgroup.go` |
| **Docs honesty sweep** — reword all "zero-downtime" claims | ✅ Done | All "zero-downtime" → "health-gated deploy with automatic rollback" |

**Moved out of old v0.4**: preview deploys, GitHub Actions, team auth, JSON logging, PostgreSQL → v0.5. Postgres stays optional and only ships if a client demands it.

## v0.5.0 — Agency → team bridge

**Goal**: Agencies hand off read-only client access as a premium tier; startup teams get SSO + CI/CD previews — bought only after trust is proven at v0.4.

- [ ] **Team auth (OIDC/OAuth2) + RBAC** — client handoff: read-only client access as a premium agency tier
- [ ] **Preview deploys + GitHub Actions integration** — the demo feature (PR → preview URL, from a YAML workflow file, no web UI)
- [x] **Audit log search/filter + retention**
- [x] **Structured JSON logging**
- [ ] **Optional PostgreSQL** — only on client demand (same schema, separate driver files)
- ~~**Multi-node**~~ — **STRUCK / deferred**: agencies run one VPS; revisit on multi-VPS demand

## v0.6.0 — Teams + monetization

**Goal**: Per-project usage/billing so agencies can bill clients, plus the managed-hosting monetization layer.

- [ ] **Per-project usage/billing** — track per-project resource usage; the feature that lets agencies bill clients and enables managed-hosting monetization
- [ ] **Webhook triggers** — deploy events (success, failure, rollback) → webhooks
- [ ] **MCP server** — AI distribution channel (NOT a wedge; deliberately late)
- ~~[ ] **Backup scheduler**~~ — **SHIPPED in v0.4.0** (scheduled per-app backups with retention, built-in daemon)
- [ ] **Route53 DNS support**
- ~~**Web dashboard**~~ — **DEFERRED** to: read-only status page max (CLI stays the product)

## Design decisions (locked)

These are decisions we've discussed and committed to. Future agents should NOT re-open these without a new discussion.

| Decision | Choice | Reasoning |
|----------|--------|-----------|
| Target order | **Dev agency → startup team → SME** | Agencies buy capability (restore/isolation/audit) not stars; teams buy only after trust is proven; solo devs are a free distribution channel, not a buyer |
| Database | SQLite default, optional PostgreSQL for teams | SQLite is zero-dependency. PostgreSQL only when team features need it |
| Redis | Not needed for v0.3-v0.4 | Only relevant for multi-node pub/sub (v0.5+) |
| CLI hierarchy | Flat commands (`deploy up`, `deploy logs`), kill `deploy app` subcommands | Less cognitive load, better for CI/CD scripts |
| Config format | deploy.yml is production config, docker-compose.yml is local dev | Clean separation, no conflict. deploy never modifies docker-compose.yml |
| Socket location | `/var/run/deploy/deploy.sock` (not `~/.deploy/`) | Standard Unix location, avoids permission issues with user home dirs |
| Socket permissions | 0770, `deploy` group | Group-based access control, daemon runs as root |
| Dockerfile generation | `deploy scaffold` auto-detects stack and generates WORKING Dockerfile | Users shouldn't need to write Dockerfiles for common stacks |
| DNS providers | ~~Keep all 6, add tests + zone extraction~~ — **REMOVED in `7d4684ec`** | DNS automation is gone. Deterministic Caddyfile; domains via `deploy domain add`, DNS configured manually (Cloudflare/registrar once) |
| DNS in init | ~~Prompted but skippable~~ — **REMOVED in `7d4684ec`** | No DNS provider prompt — init no longer touches DNS |
| Install script | Warns if Docker missing, does NOT auto-install | Too many distro-specific edge cases. User installs Docker separately |
| Promote flow | Build → stop old container → start new on the same stable port → health check → update Caddy → remove old container | **REVISED** — stop-then-start window; automatic rollback restarts the old container on health-check failure (see v0.3.x audit) |
| Port allocation | SQLite table pool + Docker real usage check | No more `app.Port + 1` collisions |
| Rollback strategy | **REVISED** — tarball default only; tag-based option REMOVED from the roadmap | The tag path is not implemented; docs honesty rule: claims must match code. Tarballs survive `docker system prune` |
| Main deploy command | `deploy up` (not `deploy promote`) | Shorter, more intuitive. `promote` becomes hidden alias |
| Scaffold approach | Auto-generate WORKING Dockerfile per stack, not example files | Example files that need editing add no value over writing from scratch |
| Install Docker | NEVER in deploy's install script | User installs Docker. Deploy warns if missing, prints link to docs. Too many distro edge cases |
| Caddy integration | Template-based snippets per app, not string replace | String replace is fragile and corrupts config on port overlaps |
| MCP server | v0.6+ (not core) | Distribution channel for AI agents, deliberately NOT a present wedge |
| Docs honesty | Claims must match code — no roadmap/README feature that doesn't exist | Trust is the only possible wedge at v0.3.x |
| Zero-downtime wording | "Health-gated deploy with automatic rollback (stop-then-start window)" until true blue/green ships | Honest about the brief stop-then-start window |
| Web dashboard | No. CLI-first is a feature, not a gap | Read-only status page is the most we'd ever ship |
| Multi-node | Deferred | Agencies run one VPS; revisit on multi-VPS demand |
| Postgres | Only on client demand | Same schema, separate driver files; SQLite stays the default |
