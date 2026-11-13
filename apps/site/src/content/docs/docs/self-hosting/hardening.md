---
title: Hardening
description: A checklist for running Lumbung where it matters.
sidebar:
  order: 6
---

Lumbung's [security model](/docs/concepts/security-model/) keeps databases apart from each other and from the host. These steps keep the instance itself safe.

## Network

- Keep the engine reachable only from this machine (the compose file publishes it on `127.0.0.1`) and put a reverse proxy with TLS in front. Never send tokens over plain HTTP across a network.
- Set `LUMBUNG_CLIENT_IP_HEADER` and `LUMBUNG_TRUSTED_PROXIES` so failed logins are limited per real client. See [Reverse proxy and TLS](/docs/self-hosting/reverse-proxy/).

## Tokens

- Don't give the service token to apps. Make one token per app, scoped to its database, with the least access it needs: `read` where it only reads.
- Limit preview and agent tokens to a branch, and give them an expiry.
- Revoke tokens you no longer use; `lumbung token list` shows when each was last used.
- Keep the data folder's `secrets` directory private. It's created with mode 700, the token file with 600.

## Host

- Run as an unprivileged user. The Docker image runs as uid 65532 on a distroless base with no shell.
- Give the container a memory limit (`mem_limit`) and keep `LUMBUNG_CONN_MAX_MEMORY` low enough that the readers and writers of your busiest branches fit inside it.
- Keep the data folder on a local disk with room to spare, and set `LUMBUNG_MAX_INSTANCE_BYTES` below the disk's size.

## Data

- Back up nightly and keep copies off the machine. Backups contain the service token: encrypt them. See [Backups](/docs/self-hosting/backups/).
- Test a restore once, and after big changes.
- Protect branches you care about besides the default one, so they can't be deleted or reset by accident.

## Updates

- Upgrade regularly; take a `--meta-only` backup first. See [Upgrades](/docs/self-hosting/upgrades/).
