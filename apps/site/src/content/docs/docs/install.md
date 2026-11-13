---
title: Install
description: Run the engine with Docker or as a single binary, and install the lumbung CLI.
---

Lumbung is two Go programs from one module: `lumbungd`, the engine, and `lumbung`, the CLI. Both are static binaries with no cgo, so they run anywhere Go builds.

## With Docker

<!-- docs-test: skip builds an image -->
```sh
docker compose up -d --build
```

The image runs as a non-root user, keeps its data in the `data` volume at `/data`, and has a built-in health check. See [Docker Compose](/docs/self-hosting/docker-compose/).

## As a binary

You need the Go version in `apps/engine/go.mod`; an older Go downloads it automatically.

<!-- docs-test: skip builds from source -->
```sh
cd apps/engine
go build -o bin/ ./cmd/lumbungd ./cmd/lumbung
LUMBUNG_DATA_DIR=/var/lib/lumbung ./bin/lumbungd
```

With no settings at all, lumbungd starts: it creates the data folder, `meta.db` and a service token, and listens on `127.0.0.1:4870`. Every setting is an environment variable; see [Configuration](/docs/reference/configuration/).

## The CLI

<!-- docs-test: skip builds from source -->
```sh
go install github.com/zakiego/lumbung/apps/engine/cmd/lumbung@latest
```

Or build it from a checkout as above. Then:

```sh
lumbung version
lumbung --help
```

## Requirements

- A local disk for the data folder. SQLite's file locking doesn't work on network file systems.
- Linux or macOS. On Linux, kernel 3.15 or newer (for open file description locks).
- Memory: about 128 MiB per busy connection at most (`LUMBUNG_CONN_MAX_MEMORY`); an idle instance uses much less.
