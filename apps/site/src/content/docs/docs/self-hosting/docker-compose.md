---
title: Docker Compose
description: Run the engine with the repository's compose.yaml, and operate it with docker compose exec.
sidebar:
  order: 1
---

The repository's `compose.yaml` runs the engine with one named volume, bound to `127.0.0.1` so only this machine can reach it.

<!-- docs-test: skip starts containers -->
```sh
docker compose up -d --build
docker compose ps
curl -s http://127.0.0.1:4870/health
```

## What's in the image

- `lumbungd` and the `lumbung` CLI, static binaries on a distroless base.
- It runs as a non-root user (uid 65532). Data lives in `/data`, the `data` volume.
- `HEALTHCHECK` runs `lumbungd healthcheck`, which asks the engine for `/health`. Compose reports the container healthy only when the engine can serve.

## Settings

Every setting is an environment variable (see [Configuration](/docs/reference/configuration/)). Add them under `environment:` in an override file, so `compose.yaml` stays as shipped:

```yaml
# compose.override.yaml
services:
  engine:
    environment:
      LUMBUNG_MAX_BRANCH_BYTES: 4GiB
      LUMBUNG_MAX_INSTANCE_BYTES: 40GiB
    mem_limit: 1g
```

## Everyday commands

<!-- docs-test: skip runs inside the container -->
```sh
docker compose exec engine lumbungd service-token     # the admin token
docker compose exec engine lumbung --help             # the CLI, inside the container
docker compose logs -f engine                         # JSON logs
docker compose exec engine lumbungd backup --to /data/backups --keep 7
```

The engine stops gracefully on `docker compose stop`: it finishes requests in flight for up to 30 seconds and flushes its stats. `stop_grace_period` is 40 seconds to cover that.

## Reaching it from elsewhere

The port is published on `127.0.0.1` only. Put a reverse proxy with TLS in front; see [Reverse proxy and TLS](/docs/self-hosting/reverse-proxy/).
