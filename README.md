# deploy — single-server personal PaaS

So I tried Coolify, Dokploy but it felt like unnecessary overhead and could never figure it out. This is my solution: lightweight, entirely in the CLI, not too hard to setup and work with. I assume it won't fit everyone's needs but I have found it to work well for what I need.

Contributions are accepted and welcomed.

Register apps, auto-assign ports, generate nginx reverse proxy config, manage processes, environment variables, automatic SSL, health checks, and rollbacks.

```bash
deploy add myapp flask /home/user/myapp
deploy config:set myapp DATABASE_URL=postgres://...
deploy start myapp -d
# -> https://myapp.yourdomain.com
```


## Quickstart

```bash
# Clone or download the repo
git clone https://github.com/yusuf-bot/deploy.git
cd deploy

# Symlink the binary
sudo ln -sf "$(pwd)/bin/deploy" /usr/local/bin/deploy

# Initialize
deploy init

# Set your domain
echo 'BASE_DOMAIN=myapp.com' >> ~/.deploy/deploy.conf

# Register and start an app
deploy add myapp node /home/user/my-node-app
deploy start myapp -d

# See everything running
deploy list
```

## Commands

| Command | Description |
|---|---|---|
| `deploy init` | Create config directory and default files |
| `deploy add <name> <type> <path>` | Register a new app (auto-assigns port) |
| `deploy remove <name>` | Unregister an app |
| `deploy list` | Show all apps, ports, and status |
| `deploy start <name> [-d]` | Start an app with health check |
| `deploy stop <name>` | Stop an app |
| `deploy restart <name>` | Stop then start |
| `deploy logs <name>` | Tail app stdout/stderr |
| `deploy info <name>` | Show app details (port, type, status, env, logs) |
| `deploy prod <name>` | Build and deploy via Docker (with rollback snapshot) |
| `deploy config:set <name> KEY=VAL` | Set an environment variable for an app |
| `deploy config:get <name> [KEY]` | List all or one env var |
| `deploy config:unset <name> KEY` | Remove an environment variable |
| `deploy config:encrypt <name>` | Encrypt env file with GPG (AES256) |
| `deploy config:decrypt <name>` | Decrypt env file with GPG |
| `deploy rollback <name>` | Rollback to previous deployment |
| `deploy ssl <name>` | Provision Let's Encrypt SSL (requires certbot) |
| `deploy ssl:renew` | Renew all Let's Encrypt certificates |
| `deploy uninstall` | Stop all apps and remove deploy data |
| `deploy nginx` | Generate nginx config and reload |
| `deploy nginx --reset` | Regenerate ignoring custom snippets |
| `deploy custom <name>` | Create/edit custom nginx snippet |
| `deploy help` | Show full usage |

## App Types

| Type | Command |
|---|---|
| `flask` | `flask run --host=0.0.0.0 --port=$PORT` |
| `laravel` | `php artisan serve --host=0.0.0.0 --port=$PORT` |
| `node` | `PORT=$PORT npm start` |
| `static` | `npx serve -s . -l $PORT` |
| `docker` | `docker compose up` |

## Configuration

All settings can go in `~/.deploy/deploy.conf`:

```bash
# Required: your domain
BASE_DOMAIN=myapp.com

# SSL (optional)
SSL_CERT=/etc/ssl/certs/myapp.crt
SSL_KEY=/etc/ssl/private/myapp.key

# Port range
MIN_PORT=8000
MAX_PORT=9000

# Nginx config output
NGINX_CONF=/etc/nginx/sites-available/deploy
```

Or set as environment variables:

```bash
BASE_DOMAIN=myapp.com deploy list
```

## Custom Nginx Config

For WebSocket support, custom locations, or advanced routing:

```bash
deploy custom myapp
# edit ~/.deploy/nginx-custom/myapp.conf
deploy nginx
```

Your custom block replaces the auto-generated default. Example with WebSocket support is in `examples/nginx-custom/`.

## How It Works

```
deploy add myapp node ./myapp
  ├── auto-assigns next available port (e.g., 8000)
  ├── writes to registry: myapp<tab>8000<tab>node<tab>app<tab>./myapp
  └── regenerates nginx config

deploy start myapp -d
  ├── runs the app's command with PORT=8000
  └── logs to ~/.deploy/logs/myapp.log

deploy nginx
  ├── reads registry, builds server blocks
  ├── uses custom snippets from ~/.deploy/nginx-custom/ if present
  ├── writes to NGINX_CONF
  └── runs nginx -t && nginx -s reload
```

## Project Structure

```
deploy/
├── bin/
│   └── deploy           # CLI entry point (thin dispatcher)
├── lib/
│   ├── core.sh          # Config, colors, helpers, nginx functions
│   ├── apps.sh          # App lifecycle: add, remove, list, start, stop, etc.
│   ├── env.sh           # Environment variable management + GPG encrypt
│   └── infra.sh         # Infrastructure: init, prod, rollback, ssl, uninstall
├── examples/
│   ├── deploy.conf.example
│   └── nginx-custom/README.md
├── .gitignore
├── LICENSE
└── README.md
```

Each lib file is a focused module. The `bin/deploy` entry point sources all libraries and dispatches commands.

## Features

| Feature | Status |
|---|---|
| App registry with auto-assigned ports | Done |
| nginx reverse proxy generation | Done |
| Custom nginx snippets per app | Done |
| Colored CLI output with status indicators | Done |
| Double-start protection | Done |
| Health checks on start (waits for app to respond) | Done |
| Environment variable management | Done |
| GPG-encrypted env files (AES256) | Done |
| Docker build + compose support | Done |
| Deployment rollback (Docker image + git) | Done |
| Let's Encrypt SSL provisioning (certbot) | Done |
| Uninstall (stops all apps, cleans config) | Done |
| Per-app info command | Done |
| Process management (start, stop, restart, logs) | Done |

## Security Notes

- The registry file controls what commands are run — protect it (`chmod 600`)
- Apps started with `-d` run via `nohup` and persist until killed or reboot
- For production, set up a process supervisor (systemd, supervisord) or use `docker` type
- SSL is optional — omit `SSL_CERT`/`SSL_KEY` to generate plain HTTP configs
- Sensitive env vars can be encrypted with `deploy config:encrypt <name>` (GPG AES256)

## Requirements

- Linux with bash
- nginx (for reverse proxy)
- lsof (for port checking)
- sudo (for nginx reload)
- curl (for health checks)
- Docker + Docker Compose (for docker/prod app types)
- certbot (optional, for Let's Encrypt SSL)
- gpg (optional, for env file encryption)

## License

MIT
