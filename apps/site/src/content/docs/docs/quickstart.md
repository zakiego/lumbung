---
title: Quickstart
description: From nothing to a first query in five minutes, with Docker Compose and the lumbung CLI.
---

<!-- docs-test: fresh -->

You need Docker with Compose, and Go to build the CLI.

## 1. Start the engine

<!-- docs-test: skip starts containers; the docs test runs the engine binary directly -->
```sh
git clone https://github.com/zakiego/lumbung.git
cd lumbung
docker compose up -d --build
```

The engine listens on `http://127.0.0.1:4870`, only on this machine. On first start it creates a service token, an instance admin.

## 2. Install the CLI and log in

<!-- docs-test: skip builds from source and reads the token inside the container -->
```sh
go install ./apps/engine/cmd/lumbung
docker compose exec engine lumbungd service-token | lumbung login --url http://127.0.0.1:4870
```

`lumbung login` saves the URL and token in your config folder, readable only by you.

## 3. Create a database and query it

```sh
lumbung db create app
lumbung query "CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL UNIQUE, name TEXT)" --db app
lumbung query "INSERT INTO users (email, name) VALUES (?, ?)" --db app --arg ana@example.com --arg Ana
lumbung query "SELECT id, email, name FROM users" --db app
```

The same query over HTTP:

```sh
curl -s http://127.0.0.1:4870/v1/databases/app/query \
  -H "Authorization: Bearer $LUMBUNG_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT id, email FROM users WHERE email = ?", "args": ["ana@example.com"]}'
```

`LUMBUNG_TOKEN` is the token you logged in with. Values come back typed, for example `{"type": "integer", "value": "1"}`; see [Value encoding](/docs/reference/value-encoding/).

## 4. Try a branch

```sh
lumbung branch create try-email-index --db app
lumbung query "CREATE INDEX users_name ON users(name)" --db app --branch try-email-index
lumbung branch diff try-email-index --db app
lumbung branch delete try-email-index --db app --yes
```

## Next

- [Connect from Node](/docs/guides/node/) with the TypeScript SDK.
- [Tokens and scopes](/docs/concepts/tokens/): make a token for each app instead of using the service token.
- [Docker Compose](/docs/self-hosting/docker-compose/) for running it for real.
