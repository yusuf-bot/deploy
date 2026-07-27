# CI/CD Integration

Deploy your application from CI pipelines using SSH to trigger deployments on the server.

## Prerequisites

- A `deploy.yml` file in your project root (see [examples](../examples/))
- The deploy daemon running on your server
- SSH key access to the server

## Server SSH Key Setup

1. Generate a deploy key (or use an existing one):

   ```bash
   ssh-keygen -t ed25519 -f ~/.ssh/deploy-key -N ""
   ```

2. Add the public key to `~/.ssh/authorized_keys` on the deploy server.

3. Copy the private key to your CI/CD secrets manager (see provider-specific sections below).

4. Test the connection:

   ```bash
   ssh -i ~/.ssh/deploy-key user@your-server "deploy status --json"
   ```

## GitHub Actions

```yaml
name: Deploy
on:
  push:
    branches: [main]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Deploy via SSH
        uses: appleboy/ssh-action@v1
        with:
          host: ${{ secrets.DEPLOY_HOST }}
          username: ${{ secrets.DEPLOY_USER }}
          key: ${{ secrets.DEPLOY_SSH_KEY }}
          script: |
            cd /path/to/app
            git pull
            deploy promote --wait .
```

## GitLab CI

```yaml
deploy:
  stage: deploy
  image: alpine:latest
  before_script:
    - apk add --no-cache openssh-client
    - eval $(ssh-agent -s)
    - echo "$DEPLOY_SSH_KEY" | tr -d '\r' | ssh-add -
    - mkdir -p ~/.ssh
    - chmod 700 ~/.ssh
  script:
    - ssh $DEPLOY_USER@$DEPLOY_HOST "
        cd /path/to/app &&
        git pull &&
        deploy promote --wait .
      "
  only:
    - main
```

## Notes

- The `deploy.yml` file must exist in your project root for `deploy promote` to work.
- Use `--wait` to block until the deployment finishes so CI catches failures.
- The deploy daemon must be running on the server (`deploy daemon`).
- Consider using `deploy status --json` for health checks in CI pipelines.
