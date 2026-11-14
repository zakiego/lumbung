# AGENTS.md

Lumbung: a self-hosted SQLite database platform with branches, an HTTP API, MCP, a CLI and a dashboard. `PLAN.md` is the source of truth for scope and behavior. Read the section you're working on before writing code.

## Layout

```
apps/engine      Go module: cmd/lumbungd (server), cmd/lumbung (CLI)
apps/dashboard   TanStack Start (Nitro) + ui-sdk + Better Auth, a client of the engine API
apps/site        Astro + Starlight: landing and docs (Cloudflare Pages)
apps/edge        Cloudflare Worker that routes our public hostname
packages/client  @lumbung/client TypeScript SDK and Drizzle adapter
examples/        Runnable examples, tested in CI (drizzle: drizzle-kit + lumbung migrate)
docs/adr         Decision records
ops/             Our own deployment files and notes
scripts/         CI helpers, worktree setup, wording check, docs tests
```

## Rules

- Lumbung is described on its own terms. No "clone of", no "alternative to", no comparison to a named hosted database product in any repo text, commit or PR. `scripts/check-wording.sh` runs in CI.
- Nothing in the product depends on our hostnames or accounts. Deployment specifics go in `ops/`.
- The isolation tests in PLAN 6.2 are never skipped, weakened or marked flaky. A failing one blocks everything.
- `apps/engine/openapi.json` is generated. Never edit it by hand. The SDK types and the API reference come from it.
- Docs ship with the feature. Code samples in docs must pass `scripts/docs-test`.
- Never type a tool or package version from memory. Use what `go version`, `node --version` and `pnpm --version` print, add dependencies with `go get` and `pnpm add`, and check current docs (Context7) before writing config.
- One heavy process at a time: installs, builds, Go tests, typechecks and Docker builds run one after another.
- English only in code, comments, commits and docs. No em dashes anywhere.
- Conventional Commits, one logical change per commit, tests passing at every commit.
- The shell hook blocks any Bash command whose text contains `.env`. Write such files with the Write or Edit tools.

## Commands

Run from the repo root. pnpm drives everything, including the Go module (`apps/engine/package.json` wraps the `go` commands), and root scripts run one package at a time.

```bash
pnpm install                          # TS dependencies (Go fetches its own on first build)
pnpm dev:engine                       # lumbungd on 127.0.0.1:4870 (scripts/dev.sh)
pnpm dev:site                         # landing and docs on localhost:4872
pnpm test                             # every package: Go tests with -race, TS tests (SDK tests build and start a real lumbungd)
pnpm typecheck                        # go vet and astro check (tsc as TS packages land)
pnpm lint                             # gofmt, go vet, staticcheck
pnpm build                            # engine binaries in apps/engine/bin, site in apps/site/dist
pnpm check:wording                    # the wording rule (CI runs it with its self-test)
pnpm e2e                              # end-to-end test (PLAN 13): builds lumbungd and the dashboard, boots both on free ports, walks setup to MCP (build the SDK first; E2E_SKIP_BUILD=1 reuses the dashboard build)
pnpm --filter @lumbung/engine vulncheck   # govulncheck
pnpm --filter @lumbung/engine openapi:gen # regenerate apps/engine/openapi.json from the handlers
pnpm --filter @lumbung/engine docs:gen    # regenerate the configuration, error code and REST API reference pages
pnpm --filter @lumbung/engine sdk:gen     # regenerate packages/client/src/generated.ts from openapi.json
```

After changing an API handler, a setting or an error code, run `openapi:gen`, then `docs:gen` and `sdk:gen`, and commit the results: CI fails when either differs from a fresh generation. API tests compare error bodies with `apps/engine/internal/api/testdata/golden`; rewrite them with `go test ./internal/api/ -update` only when a change to an error is intended.

Inside `apps/engine` the plain Go commands work too: `go test -race ./...`, `go tool staticcheck ./...`, `go tool govulncheck ./...`. `go.mod` pins Go 1.27.4 (ADR 0002); an older local Go downloads it on first use.

Dev ports: engine 4870, dashboard 4871, site 4872 in the main checkout. A linked worktree gets its own block of three in 4900-4998, picked from its path by `scripts/dev.sh`, which prints the URL first.

T3 worktrees: `scripts/setup-worktree.sh` copies gitignored env files from the main checkout and installs pnpm and Go dependencies. `scripts/clean-worktree.sh` deletes dependencies and build output in a settled worktree and refuses to run in the main checkout. Actions in `t3.json` only take effect after importing them in Settings > Projects > Actions (already done for this project).
