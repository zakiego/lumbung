# 0001: SQLite driver

Date: 2026-11-14. Status: accepted.

## Context

PLAN 6.1 asks for a Go SQLite driver picked by a spike, with a test for each of six capabilities: a per-connection authorizer, runtime limits, cancelling a running statement from a context, an online copy of a live database, read-only connections, and JSON1 plus FTS5.

The candidates were `mattn/go-sqlite3` (cgo) and `ncruces/go-sqlite3` (no cgo).

## Decision

Use `github.com/ncruces/go-sqlite3` v0.37.0, which embeds SQLite 3.54.1 (translated to Go with wasm2go).

The spike is `apps/engine/internal/sqlitex/driver_test.go`. Each test runs against the raw driver, so an upgrade that drops a capability fails there first:

| Capability | Test | Driver API |
|---|---|---|
| Authorizer that can deny | `TestDriverAuthorizer` | `Conn.SetAuthorizer` |
| Runtime limits | `TestDriverLimits` | `Conn.Limit` |
| Cancel from a context | `TestDriverInterrupt` | `Conn.SetInterrupt` |
| Online copy | `TestDriverBackup` | `Conn.Backup` |
| Read-only connections | `TestDriverReadOnly` | `mode=ro` / `OPEN_READONLY` |
| JSON1 and FTS5 | `TestDriverJSONAndFTS5` | built in; `ext/fts5.Register` per connection |

Things the spike found that shape the engine:

- `load_extension` isn't compiled in at all (`TestDriverHasNoLoadExtension`).
- `VACUUM INTO` reaches the authorizer as an attach of its target file, so denying `SQLITE_ATTACH` also denies it. A zero attach limit blocks it too.
- The interrupt leaves the connection usable once the context is reset.
- The `DBCONFIG_ENABLE_ATTACH_CREATE` and `DBCONFIG_ENABLE_ATTACH_WRITE` options are rejected by this build. Attach is refused three other ways, so they aren't needed.

## Why this one over mattn/go-sqlite3

- Its VFS is Go code that can be wrapped. Each user connection gets a VFS that only opens that branch's own files (`internal/sqlitex/vfs.go`), a layer under the authorizer that cgo SQLite doesn't offer without writing a C VFS.
- No cgo: the engine builds as a static binary, cross-compiles, and the Docker build stage needs no C toolchain, which matters with a 3 GB build VM.
- Each connection has a hard memory ceiling (`WithMaxMemory`), so one runaway statement can't take the process down.

mattn/go-sqlite3 also provides the six capabilities. It wasn't spiked further because the confined VFS and the memory ceiling are worth more here than its raw speed.

## Costs

- Slower than native SQLite for heavy queries, and each connection uses more memory. Pools stay small (one writer, a few readers per branch) and idle connections close.
- Locking uses OFD locks on Linux and macOS. The data volume must be a local file system, which the Docker named volume is.
