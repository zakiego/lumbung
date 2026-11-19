# Contributing

Thanks for taking a look. Lumbung is a small project, so open an issue before a big change and we can agree on the approach first. Small fixes can go straight to a pull request.

## Setup

You need Node (version in `.nvmrc`), pnpm (version in `package.json`) and Go (version in `apps/engine/go.mod`, an older Go downloads it on first use).

```bash
pnpm install
pnpm dev:engine      # lumbungd on 127.0.0.1:4870
pnpm dev:site        # landing and docs on localhost:4872
```

## Before you open a PR

```bash
pnpm lint
pnpm typecheck
pnpm test
pnpm check:wording
```

`AGENTS.md` lists every command, including the end-to-end test and the generators.

## Rules that CI checks

- `apps/engine/openapi.json`, the API reference pages and `packages/client/src/generated.ts` are generated. After changing an API handler, a setting or an error code, run `openapi:gen`, `docs:gen` and `sdk:gen` and commit the output.
- Docs ship with the feature, and code samples in the docs must pass `pnpm docs:test`.
- Describe Lumbung on its own terms. No comparisons to named hosted database products in code, docs, commits or PR text. `scripts/check-wording.sh` enforces it.
- The isolation tests (one database or workspace never reaches another) are never skipped or weakened.

## Commits

Conventional Commits (`fix(engine): ...`, `feat(dashboard): ...`), one logical change per commit, tests passing at every commit.

## License

By contributing you agree that your contribution is licensed under the [MIT License](LICENSE).
