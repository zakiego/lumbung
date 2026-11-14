---
title: Docker Compose
description: Run the engine and the dashboard with the repository's compose.yaml, and operate them with docker compose exec.
sidebar:
  order: 1
---

The repository's `compose.yaml` runs the engine and the dashboard, each with its own volume, bound to `127.0.0.1` so only this machine can reach them.

<!-- docs-test: skip starts containers -->
```sh
docker compose up -d --build
docker compose ps
curl -s http://127.0.0.1:4870/health
docker compose exec engine lumbungd service-token
```

Open `http://localhost:4871/app` and set up the owner with that token (see [Use the dashboard](/docs/guides/dashboard/)).

## What's in the engine image

- `lumbungd` and the `lumbung` CLI, static binaries on a distroless base.
- It runs as a non-root user (uid 65532). Data lives in `/data`, the `data` volume.
- `HEALTHCHECK` runs `lumbungd healthcheck`, which asks the engine for `/health`. Compose reports the container healthy only when the engine can serve.

## The dashboard image

- A Node server built from `apps/dashboard`. It runs as the same uid as the engine, so it can read the engine's generated service token from the `data` volume, mounted read-only at `/engine`.
- Its own data (`dashboard.db` with users, sessions and invites, plus a generated session secret) lives in `/data`, the `dashboard` volume.
- `HEALTHCHECK` asks `/app/api/health`. The dashboard waits for the engine to be healthy before it starts.

## Settings

Every setting is an environment variable (see [Configuration](/docs/reference/configuration/) and [Dashboard configuration](/docs/reference/dashboard-configuration/)). Add them under `environment:` in an override file, so `compose.yaml` stays as shipped:

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

Both ports are published on `127.0.0.1` only. Put a reverse proxy with TLS in front; see [Reverse proxy and TLS](/docs/self-hosting/reverse-proxy/).
