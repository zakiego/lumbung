# Changelog

## 0.2.0

For engines with workspaces. A workspace owns databases, buckets and tokens, and a token for one can't see anything outside it.

### Breaking

- `tokens.create` without `database`, `bucket` or `grants` makes a **workspace token** in the workspace the client acts in. It used to make an instance token, which reached everything on the instance. Make instance tokens with `instance.tokens.create`.
- `audit.list` is the audit log of the workspace the client acts in. The log of the whole instance is `instance.audit.list`.
- `tokens.list` no longer holds instance tokens, the dashboard's service token included. They are in `instance.tokens.list`.
- A token's `scope` may be `"workspace"`. Code that switches on `scope` needs a case for it.
- This version needs an engine with workspaces. Against an older engine the new calls answer `not_found`.

Nothing changes for a client that only queries: URLs are the same, and a database, bucket or grants token works as before.

### Added

- `createClient({ workspace })` and `client.workspace(name)`: act in a workspace by name or id. Only an instance token needs it; a workspace token always acts in its own.
- `client.workspaces`: `current`, `list`, `trash`, `get`, `create`, `update`, `delete`, `restore`.
- `client.instance.tokens` (`list`, `create`, `revoke`) and `client.instance.audit.list`.
- `databases.move` and `buckets.move`, to put a database or a bucket in another workspace.
- `workspace_id` on database, bucket, token and audit entries, and the `WorkspaceOut` and `WorkspaceLimits` types.

## 0.1.2

- Tokens with several grants: `tokens.create({ grants })`.

## 0.1.1 and 0.1.0

- The first releases: `query`, `batch`, management calls, buckets and objects, and the Drizzle adapter.
