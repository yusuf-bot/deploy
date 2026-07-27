# Roadmap

## v0.2.0 (Current)

- Docker-based deploy pipeline with health checks, rollbacks, and audit logging
- Caddy reverse proxy with automatic Let's Encrypt SSL
- Encrypted secrets (AES-256-GCM) and daemon settings
- Development containers with volume mounts (`deploy dev start/stop`)
- Git push deploy workflow (`deploy git setup`)
- DNS automation for 6 providers (Cloudflare, DigitalOcean, Hetzner, Linode, Porkbun, Vultr)
- Backup, restore, uninstall, and release infrastructure
- 45 CLI commands

## v0.3.0 (Next)

### Polish & Hardening

- **Integration testing** — Docker-based integration tests for promote, rollback, and dev container lifecycle. Run in CI with Docker-in-Docker or socket mount.
- **Settings test coverage** — Write unit tests for `state/settings.go` (EncryptedSetSetting, EncryptedGetSetting, GetAllSettings).
- **DNS provider test coverage** — Mock HTTP tests for all 6 providers, covering API errors, timeouts, and edge cases.
- **Command test coverage** — Unit tests for `cmd/app/` and `cmd/*.go` command wiring and validation logic.
- **HTTP status code checks** — Add proper HTTP status code validation to all DNS provider API calls (not just API-level error fields).
- **JSON decode error checks** — Check all `json.Decode` return values in DNS providers for better error messages.

### Features

- **Health endpoint** — Daemon health check endpoint (`/health` or similar) for monitoring and uptime checks.
- **Graceful shutdown on signal** — Proper SIGTERM/SIGINT handling in the daemon: drain in-flight requests, save state, stop containers gracefully.
- **State file corruption recovery** — Automatic recovery or repair tool for SQLite state corruption.
- **Port tracker table** — SQLite-backed port allocation tracker to prevent port collisions across apps during concurrent promotes. Global lock for port-sensitive operations.

### Security

- **Memory scrubbing for secrets** — Use `[]byte` with explicit clearing (`runtime.MemclrNoHeapPointers`) for decrypted secret values instead of immutable Go strings. Mitigates secrets leaking via memory dumps or swap.
- **Secrets rotation** — CLI command to rotate the master key and re-encrypt all stored secrets.
- **Rate limiting on socket API** — Prevent brute-force attempts on the Unix socket API.

## v0.4.0+ (Future)

- MCP (Model Context Protocol) server for AI-assisted deploy management
- Preview deployments (ephemeral per-branch environments)
- Multi-node support (cluster management)
- Web dashboard (read-only monitoring)
- OIDC/OAuth2 authentication for the socket API
- Prometheus metrics endpoint
- Structured logging (JSON format)
