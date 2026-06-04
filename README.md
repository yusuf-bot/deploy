# deploy — single-server personal PaaS

So I tried Coolify, Dokploy but it felt like unnecesary overhead and could never figure it out. So this is my solution lightweight, entirely in the cli, not too hard to setup and work with. I assume it won't fit everyone's needs but I have found it to work well for what I need.

Contributions are accepted and welcomed.

Do what you want.

Register apps, auto-assign ports, generate nginx reverse proxy config, manage processes.

```bash
deploy add myapp flask /home/user/myapp
deploy start myapp -d
# → https://myapp.yourdomain.com
```


## Quickstart

```bash
# Download
curl -O https://raw.githubusercontent.com/YOUR_USER/deploy/main/deploy.sh
chmod +x deploy.sh
sudo mv deploy.sh /usr/local/bin/deploy

# Initialize
deploy init

# Set your domain
echo 'BASE_DOMAIN=myapp.com' >> ~/.deploy/deploy.conf

# Register and start an app
deploy add myapp node /home/user/my-node-app
deploy start myapp -d

# Generate nginx config
deploy nginx

# See everything running
deploy list
```

## Commands

| Command | Description |
|---|---|
| `deploy init` | Create config directory and default files |
| `deploy add <name> <type> <path>` | Register a new app (auto-assigns port) |
| `deploy remove <name>` | Unregister an app |
| `deploy list` | Show all apps, ports, and status |
| `deploy start <name> [-d]` | Start an app (daemonize with `-d`) |
| `deploy stop <name>` | Stop an app by port |
| `deploy restart <name>` | Stop then start |
| `deploy logs <name>` | Tail app stdout/stderr |
| `deploy prod <name>` | Docker Compose deploy (`--build -d`) |
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

## Security Notes

- The registry file controls what commands are run — protect it (`chmod 600`)
- Apps started with `-d` run via `nohup` and persist until killed or reboot
- For production, set up a process supervisor (systemd, supervisord) or use `docker` type
- SSL is optional — omit `SSL_CERT`/`SSL_KEY` to generate plain HTTP configs

## Requirements

- Linux with bash
- nginx (for reverse proxy)
- lsof (for port checking)
- sudo (for nginx reload)
- Node.js `serve` package for `static` type apps

## License

MIT
