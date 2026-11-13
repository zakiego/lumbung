# 0002: Go toolchain 1.27

Date: 2026-11-14. Status: accepted.

## Context

The machine the build started on has Go 1.25.6. On this date go.dev lists 1.27.4 and 1.26.11 as the supported releases, so 1.25 no longer gets security fixes. govulncheck reports standard library vulnerabilities against the toolchain a module builds with, which would fail CI on 1.25. The current staticcheck (v0.8.2) also needs Go 1.26 or newer.

## Decision

`apps/engine/go.mod` declares `go 1.27.4`. A local Go older than that downloads the 1.27.4 toolchain on first use (the default `GOTOOLCHAIN=auto`). CI reads the version from `go.mod` through `actions/setup-go`'s `go-version-file`. The Docker build stage (milestone 10) must use the matching `golang` image.

staticcheck and govulncheck are pinned as `tool` directives in `go.mod` and run with `go tool`, so CI and local runs use the same versions.

## Consequences

Bumping Go is a one-line change to `go.mod` plus the Docker base image. Check go.dev for the current release before changing it, never a remembered version.
