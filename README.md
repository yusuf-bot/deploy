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

# Edit config
nano ~/.deploy/deploy.conf

# Register and start an app
deploy add myapp node /home/user/my-node-app
deploy start myapp -d

# See everything running
deploy ls
```

## Commands

| Command | Description |
|---|---|
| `deploy init` | Create config directory and default files |
| `deploy add <name> <type> <path>` | Register a new app (auto-assigns port) |
| `deploy remove\|rm <name>` | Unregister an app |
| `deploy ls` | Show all apps and their status |
| `deploy start <name> [-d]` | Start an app with health check |
| `deploy stop <name>` | Stop an app (SIGTERM → wait → SIGKILL) |
| `deploy restart <name>` | Stop then start |
| `deploy dev <name>` | Start in dev mode (hot reload, dev subdomain) |
| `deploy dev:init <name> <template>` | Scaffold a new project (flask, express, node, static) |
| `deploy dev:logs <name>` | Tail dev mode logs with file watching |
| `deploy dev:url <name> [--open]` | Show dev URL, optionally open in browser |
| `deploy status [name]` | Show status for all or one app |
| `deploy logs <name>` | Tail app stdout/stderr |
| `deploy info <name>` | Show app details (port, type, status, env, logs) |
| `deploy prod <name>` | Build and deploy via Docker (with rollback snapshot) |
| `deploy up [path]` | Deploy from deploy.yml in current directory |
| `deploy config:set <name> KEY=VAL` | Set an environment variable for an app |
| `deploy config:get <name> [KEY]` | List all or one env var |
| `deploy config:unset <name> KEY` | Remove an environment variable |
| `deploy config:encrypt <name>` | Encrypt env file with GPG (AES256) |
| `deploy config:decrypt <name>` | Decrypt env file with GPG |
| `deploy env:setup [name]` | Generate .env.example from app config |
| `deploy rollback <name>` | Rollback to previous deployment |
| `deploy ssl <name>` | Provision Let's Encrypt SSL (requires certbot) |
| `deploy ssl:renew` | Renew all Let's Encrypt certificates |
| `deploy nginx` | Generate nginx config and reload |
| `deploy nginx --reset` | Regenerate ignoring custom snippets |
| `deploy custom <name>` | Create/edit custom nginx snippet |
| `deploy help` | Show full usage |

## App Types

| Type | Start Command | Dev Command |
|---|---|---|
| `flask` | `flask run --host=0.0.0.0 --port=$PORT` | `flask run --debug --host=0.0.0.0 --port=$PORT` |
| `laravel` | `php artisan serve --host=0.0.0.0 --port=$PORT` | `php artisan serve --host=0.0.0.0 --port=$PORT` |
| `node` | `PORT=$PORT npm start` | `PORT=$PORT npm run dev` |
| `static` | `npx serve -s . -l $PORT` | `npx serve -s . -l $PORT --no-clipboard` |
| `docker` | `docker compose up --build -d` | `docker compose up --build -d` |
| `custom` | From deploy.yml `start` field | From deploy.yml `dev` field |

## Dev Mode

```bash
deploy dev myapp
# Starts the app with dev flags on a separate port
# Available at: https://myapp.dev.yourdomain.com
# Hot reload enabled for supported types
```

`deploy dev` uses a separate nginx config (`/etc/nginx/sites-available/deploy-dev`) so dev and prod run side by side. Regenerate it with `deploy nginx`.

## deploy.yml — In-App Config

Instead of passing flags every time, put a `deploy.yml` in your project root:

```yaml
# deploy.yml
name: myapp
type: flask
start: flask run --host=0.0.0.0 --port=$PORT
dev: flask run --debug --host=0.0.0.0 --port=$PORT
websocket: false
health:
  path: /health
env:
  DATABASE_URL: postgres://user:pass@localhost/db
```

Then deploy with one command:

```bash
cd ~/myapp
deploy up
```

Auto-registers, starts, creates DNS, generates nginx, provisions SSL. See `examples/deploy.yml.example`.

## DNS Automation

To auto-create DNS records (no more Cloudflare dashboard trips):

```bash
# ~/.deploy/deploy.conf
DNS_PROVIDER=cloudflare
CLOUDFLARE_TOKEN=your_api_token
CLOUDFLARE_ZONE=yourdomain.com
```

Then `deploy add` and `deploy dev` automatically create A records. Remove an app and the DNS record is cleaned up. Supports manual mode (prints instructions) when no provider is set.

### Supported Providers

| Provider | Config | Status |
|---|---|---|
| Cloudflare | `CLOUDFLARE_TOKEN` + `CLOUDFLARE_ZONE` | Done |
| Manual | No config needed | Done (prints instructions) |

## WebSocket Support

Enable WebSocket proxying per app:

```bash
# Via config file
echo "websocket=true" >> ~/.deploy/apps/myapp/config
deploy nginx

# Or via deploy.yml
echo "websocket: true" >> deploy.yml
deploy up
```

## PID Management (No More False "Stopped")

The status detection now uses three methods:
1. **Stored PID** — checks the saved PID file first
2. **Port lsof** — fallback using `lsof -ti :port`
3. **Docker ps** — for docker-type apps

Stop uses a proper flow: `SIGTERM → wait 10s → SIGKILL` if needed.

## Configuration

All settings go in `~/.deploy/deploy.conf`:

```bash
# Required: your domain
BASE_DOMAIN=myapp.com

# Dev domain (defaults to dev.$BASE_DOMAIN)
DEV_DOMAIN=dev.myapp.com

# DNS automation
DNS_PROVIDER=cloudflare
CLOUDFLARE_TOKEN=abc123
CLOUDFLARE_ZONE=myapp.com

# SSL paths
SSL_CERT=/etc/letsencrypt/live/myapp.com/fullchain.pem
SSL_KEY=/etc/letsencrypt/live/myapp.com/privkey.pem

# Port range for apps
MIN_PORT=8000
MAX_PORT=9000

# Nginx config output
NGINX_CONF=/etc/nginx/sites-available/deploy
NGINX_DEV_CONF=/etc/nginx/sites-available/deploy-dev
```

## How It Works

```
deploy add myapp node ./myapp
  ├── auto-assigns next available port
  ├── writes to registry
  ├── creates DNS record (if configured)
  └── regenerates nginx config

deploy start myapp -d
  ├── runs the app's command
  ├── saves PID for accurate status tracking
  └── health check waits for app to respond

deploy stop myapp
  ├── sends SIGTERM
  ├── waits 10 seconds
  └── sends SIGKILL if still alive

deploy up
  ├── reads deploy.yml
  ├── registers if needed
  ├── sets env vars
  ├── creates DNS record
  ├── starts the app
  ├── generates nginx config
  └── provisions SSL
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
│   ├── infra.sh         # Infrastructure: init, prod, dev, rollback, ssl
│   ├── dns.sh           # DNS provider abstraction (Cloudflare, manual)
│   └── yml.sh           # deploy.yml parser + deploy up command
├── examples/
│   ├── deploy.conf.example
│   ├── deploy.yml.example
│   └── nginx-custom/
├── LICENSE
└── README.md
```

## Requirements

- Linux with bash
- nginx (for reverse proxy)
- lsof (for port/PID checking)
- sudo (for nginx reload)
- curl (for health checks)
- Docker + Docker Compose (for docker/prod app types)
- certbot (optional, for Let's Encrypt SSL)
- gpg (optional, for env file encryption)

## License

MIT
