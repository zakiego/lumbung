# 0007: Tokens with several grants

Date: 2026-11-14. Status: accepted.

## Context

A token had one scope: the instance, one database (optionally one branch) or one bucket. An app or an AI agent that keeps rows in its own database and files in its own bucket then needed either two tokens or an instance token. Two tokens don't fit clients that take one credential: an MCP client is configured with one URL and one bearer token, so the agent would need two connections and would see two sets of the same tools. An instance token reaches every database and bucket on the instance, which is the opposite of what an agent should hold.

PLAN 16.4 said a database token never reaches a bucket and the reverse. That rule exists so a token can't reach something nobody gave it. It doesn't forbid giving one token both on purpose.

## Decision

A token may hold a list of **grants**. A grant names one database (optionally one branch of it) or one bucket, with its own access level. A token with two or more grants has the scope `grants`. Instance, database and bucket tokens are unchanged, and a request for exactly one grant is stored as a plain database or bucket token, so each reach has one representation.

### Checks

`auth.Principal` answers every access question through its methods, and those now loop over the principal's grants. A database or bucket principal has exactly one grant, built from its own fields, so the old scopes run through the same code as the new one instead of beside it.

- A grant never lends its level to another: write on a database gives nothing on a bucket granted at read.
- Something no grant names is not found, the same answer as something that doesn't exist.
- A branch-limited grant reaches only its branch. The two places that read the principal's branch directly now ask `WholeDatabase` and `SeesBranch`.
- The stored `access` of a grants token is the highest level among its grants, for display. `FromToken` leaves the principal's `Access` empty for this scope, so code that read it directly would fail closed.

### Minting and managing

`CoversToken` requires the caller to be an admin of every grant the other token has. Admin of one database a token reaches is not enough to list or revoke a token that also reaches a bucket. This is stricter than "any shared grant" on purpose: revoking a credential someone else depends on for a resource you don't own is a change outside your scope, and listing it would reveal that the resource exists.

### Lifetime

Deleting a branch or a bucket revokes every token with a grant on it, and purging a database removes them, the same as for single-scope tokens. The whole token goes, not just the grant.

This means an admin of one branch, by deleting it, ends a token that also reaches things that admin can't see, and that `CoversToken` wouldn't let them revoke. Accepted: the branch is theirs to delete, a credential can't outlive something it was made for without changing meaning, and the alternative (refusing the delete) would tell them such a token exists. A token meant to survive a branch should not be granted that branch.

A database in the trash keeps its tokens, as before: the grant answers not found, the token's other grants keep working, and a restore brings the grant back. The token only goes when the database is purged.

Considered: dropping only the affected grant and keeping the token alive. Rejected because the token would silently change what it reaches, and a consumer that needs both halves would half-work instead of failing clearly.

### Storage and API

Migration 5 adds `token_grants` (token id, position, database id, branch id, bucket id, access) and rebuilds `tokens` again, since its scopes are named in a CHECK constraint. Grants are only read for a token with the scope `grants`, so authenticating a single-scope token costs what it did.

`POST /v1/tokens` takes `grants` in place of `database`, `branch`, `bucket` and `access`, up to 20 and no two for the same target. A whole-database grant and a branch grant on the same database are refused together: the wider one would decide every check, so the narrower one would only look like a limit. `TokenOut` gains `grants` for every token (one entry for a database or bucket token, none for an instance token), so a client can render any token the same way. The CLI takes a repeatable `--grant`, the SDK a `grants` option, and the dashboard's token dialog adds a row per grant.

Considered: a separate "agent" or "service account" record that groups several single-scope tokens. Rejected: the consumer still gets several credentials, and the grouping adds a concept without changing what any credential can do.

Considered: a database token with one optional bucket at the same level. Rejected as too narrow: it couldn't express a database at write and a bucket at read, or two databases.

## Consequences

- PLAN 6.8, 16.3, 16.4 and 16.5 are updated, and 16.5 gains a row. Its tests are `TestGrantsIsolationREST`, `TestGrantsIsolationS3`, `TestGrantsIsolationLibSQLAndMCP`, `TestGrantsBranchLimit` and `TestGrantsTokenRules`. The existing isolation tests are unchanged and still pass.
- Every request by a grants token loads its grants with one more query on meta.db. The list is capped at 20.
- A client that switches on `scope` sees a new value, `grants`.
- A later migration that rebuilds `tokens` must carry `token_grants` across: dropping `tokens` cascades into it. Migration 5 says so in a comment.
