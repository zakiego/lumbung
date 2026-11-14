# ops: our deployment

How we run Lumbung at https://lumbung.zakiego.com on the Mac mini (the `zaki-selfhost` skill). None of this is part of the product. The generic setup is `compose.yaml` and the Self-hosting docs.

## What runs where

| Thing | Where |
|---|---|
| Checkout | `~/selfhost/lumbung` (compose project `lumbung`) |
| Compose files | `compose.yaml` + `ops/compose.selfhost.yaml` (ports, `mem_limit`, logs, public URLs, proxy header) |
| Engine | `127.0.0.1:4805` (container 4870), 1 GB, volume `lumbung_data` |
| Dashboard | `127.0.0.1:4806` (container 4871), 512 MB, volume `lumbung_dashboard`. Also signs in at http://127.0.0.1:4806/app and http://localhost:4806/app on the Mac mini (password only, no passkeys), for when the tunnel is down |
| Site | Cloudflare Pages, `SITE_ORIGIN` in `apps/edge/wrangler.jsonc` |
| Edge Worker | `apps/edge`, route `lumbung.zakiego.com/*` |
| Nightly backup | launchd `com.zakiego.lumbung-backup`, 03:30, log `~/selfhost/lumbung-backup.log` |

Requests go Worker first. `/app`, `/v1`, `/mcp`, `/libsql` and `/health` pass to the zone origin (the tunnel), everything else to Pages. The tunnel splits the app paths by port, with the path rule above the host rule:

```yaml
  # Lumbung dashboard, must stay above the engine rule (zaki-selfhost)
  - hostname: lumbung.zakiego.com
    path: ^/app(/|$)
    service: http://127.0.0.1:4806

  # Lumbung engine (zaki-selfhost)
  - hostname: lumbung.zakiego.com
    service: http://127.0.0.1:4805
```

Every compose command here needs both files. The scripts set this themselves:

```bash
cd ~/selfhost/lumbung
export COMPOSE_FILE=compose.yaml:ops/compose.selfhost.yaml
```

## First deploy

1. Clone and deploy. The first deploy skips the backups (no volumes yet), and `pre-deploy.sh` installs the nightly backup agent.

   ```bash
   gh repo clone zakiego/lumbung ~/selfhost/lumbung
   ~/.agents/skills/zaki-selfhost/scripts/deploy.sh lumbung
   ```

2. Hostname: `add-host.sh` adds the engine rule and the CNAME. Then add the dashboard path rule above it by hand (`add-host.sh` refuses a host that already has a rule), validate, and Zaki restarts the tunnel.

   ```bash
   ~/.agents/skills/zaki-selfhost/scripts/add-host.sh lumbung.zakiego.com 4805 "Lumbung engine"
   cloudflared tunnel --config ~/.cloudflared/named-tunnel.yml ingress validate
   ```

3. Site and Worker: create the Pages project for `apps/site`, put its real `*.pages.dev` hostname in `SITE_ORIGIN`, then check the preview before taking the route.

   ```bash
   pnpm --filter @lumbung/edge deploy:preview
   pnpm --filter @lumbung/edge deploy
   ```

4. Owner: print the service token and set up the owner at https://lumbung.zakiego.com/app.

   ```bash
   docker compose exec engine lumbungd service-token
   ```

5. Create one database with a table, run the backup once, then the restore drill.

   ```bash
   launchctl kickstart gui/$(id -u)/com.zakiego.lumbung-backup
   tail -n 20 ~/selfhost/lumbung-backup.log
   ./ops/restore-drill.sh
   ```

6. Add `https://lumbung.zakiego.com/health` to the `http` list in `~/.agents/skills/m4-alerts/config.default.json`, the row to the Apps table in the `zaki-selfhost` skill, and `"zakiego/lumbung": "lumbung"` to its `webhook/apps.json`.

## Deploy and roll back

A push to `main` deploys (once allowlisted). By hand:

```bash
~/.agents/skills/zaki-selfhost/scripts/deploy.sh lumbung
```

`deploy.sh` runs `scripts/pre-deploy.sh` (cheap checks: docker answers, `meta.db` is one the running engine can serve, the nightly backup is installed), then `scripts/deploy-up.sh`: it builds the engine, then the dashboard, backs up `meta.db` and `dashboard.db` right before the swap with the old images, and swaps.

A `meta.db` backup must be one the old engine can restore: its manifest's `meta_version` can't be higher than the migrations that engine knows. When it is (a `meta.db` already migrated past the running engine), the copy is removed and the deploy stops before the swap. `LUMBUNG_ALLOW_NO_ROLLBACK=1` deploys anyway, for when the new engine is the fix.

When health fails, `deploy-up.sh` rolls back what failed, and the deploy still counts as failed (it alerts):

- New engine healthy, dashboard not: only the dashboard goes back to `:prev`. The new engine keeps serving.
- Otherwise both go back to `:prev`. If the new engine migrated `meta.db` past what the old one knows (the old one would answer 503 on `/health`), `ops/rollback-meta.sh` first takes a full backup of every branch, then restores `meta.db` from the copy taken right before the swap.

Going back one image after a healthy deploy:

```bash
docker tag lumbung-engine:prev lumbung-engine:latest
docker tag lumbung-dashboard:prev lumbung-dashboard:latest
./ops/rollback-meta.sh
docker compose up -d --no-build --wait
```

`rollback-meta.sh` changes nothing when the engine image can serve `meta.db` as it is. When it can't, it stops both services, takes a full backup of every branch to `lumbung_data:/data/backups/pre-rollback/<time>` (kept until you remove it), and restores `meta.db` from the newest pre-deploy copy that image knows. `--from /data/backups/pre-deploy/<time>-meta` picks a copy. The `meta.db` it replaced stays next to it as `meta.db.before-restore-<time>`.

### What a meta-only rollback loses

`meta.db` goes back to the moment of the copy, and the engine forgets everything recorded after it:

- Databases, branches and snapshots created after the copy. Their files have no record any more, and the engine deletes them from the data folder when it starts.
- Changes to records after the copy: renames, deletions, tokens created or revoked, settings.

Rows written to databases that existed at the copy stay: their branch files aren't touched. After an automatic rollback only what the failed engine wrote is lost, since the copy is from right before the swap. A rollback by hand days after a deploy can lose much more, so check what changed first.

### Getting data back

The full backup in `pre-rollback/<time>` holds every branch file (`dbs/<database id>/<branch id>.db`) and the `meta.db` from before the rollback, which names them. Copy it out of the volume (it holds the service token, so not into the checkout), find what was lost and import its branch file into a new database with the `lumbung` CLI, logged in to the engine:

```bash
docker compose cp engine:/data/backups/pre-rollback/<time> ~/selfhost/lumbung-pre-rollback
sqlite3 -readonly ~/selfhost/lumbung-pre-rollback/meta.db \
  "SELECT d.name, b.name, 'dbs/' || d.id || '/' || b.id || '.db' FROM databases d JOIN branches b ON b.database_id = d.id"
lumbung db create <name>
lumbung import ~/selfhost/lumbung-pre-rollback/dbs/<database id>/<branch id>.db --db <name>
rm -rf ~/selfhost/lumbung-pre-rollback
```

Once a fixed engine is deployed, the whole full backup can also go back into a fresh volume (Full restore, below), which drops everything written after the rollback instead. Remove the folder when you're done with it:

```bash
docker run --rm --network none --user 65532 -v lumbung_data:/data --entrypoint rm lumbung-dashboard:latest -rf /data/backups/pre-rollback/<time>
```

## Palui's agent

Palui's agent keeps its own rows and files here through MCP, with one token that reaches only its database and its bucket (ADR 0007).

1. In the dashboard: **Tokens**, **New token**. Pick the agent's database with **Read and write**, **Add another**, pick its bucket, then create. Skip either one if the agent only needs the other.
2. In Palui: add an **MCP server** connector with the MCP URL and the token shown after creating, and bearer auth.
3. Pick which tools Palui may use. `execute`, `put_object`, `delete_object` and the branch tools change data, so Palui asks before running them.

Palui treats MCP servers as external, so a turn that read from Lumbung asks before it writes anywhere.

## Backups

| Kind | Where | Kept |
|---|---|---|
| Pre-deploy `meta.db` | `lumbung_data:/data/backups/pre-deploy/<time>-meta` | 5 good ones |
| Pre-deploy `dashboard.db` | `lumbung_dashboard:/data/backups/pre-deploy-<time>.db` and `.auth-secret` | 5 |
| Before a meta-only rollback, every branch | `lumbung_data:/data/backups/pre-rollback/<time>` | until removed by hand |
| Nightly, full engine | `lumbung_data:/data/backups/nightly/<time>` and `~/selfhost/lumbung-backups/engine/<time>` | 7 each |
| Nightly `dashboard.db` | `lumbung_dashboard:/data/backups/nightly-dashboard-<time>.db` and `.auth-secret`, and `~/selfhost/lumbung-backups/dashboard/` | 7 each |

Each `dashboard.db` copy has the dashboard's generated sign-in secret (`secrets/auth-secret`) next to it as `<name>.auth-secret`. `dashboard.db` restored without its secret signs everyone out.

The host copies hold the service token and the sign-in secret (folder mode 700, secret files 600). They survive a lost volume, not a lost disk.

Every deploy checks the nightly agent (`ops/install-backup.sh --check`) and reinstalls it when the installed script or plist differs from the repo or launchd doesn't have the job loaded. A failed install warns in the deploy log and the next deploy tries again. By hand:

```bash
./ops/install-backup.sh --check || ./ops/install-backup.sh
```

Restore drill: restores the newest host copy into scratch volumes, starts a throwaway engine on a free port in 4890-4899, runs `integrity_check` on every database through the API, tears it all down and prints PASS or FAIL.

```bash
cd ~/selfhost/lumbung && ./ops/restore-drill.sh
```

Full restore after losing `lumbung_data`: into a fresh, empty volume, then start as usual.

```bash
docker compose down
docker volume rm lumbung_data
docker compose run --rm --no-deps -v ~/selfhost/lumbung-backups/engine:/backups:ro engine restore --from /backups --to /data
docker compose up -d --no-build --wait
```

The bind-mounted files must be readable by uid 65532. If they aren't, copy them in the way `restore-drill.sh` does.

Restoring `dashboard.db`: with the dashboard stopped, put a copy and its `.auth-secret` in place. The argument is the copy's path without the extension: `/b/nightly-dashboard-<time>` for a host copy, `/data/backups/pre-deploy-<time>` for one in the volume. The files it replaces go to `/data/backups/before-restore-<time>` in the volume.

```bash
docker compose stop dashboard
docker run --rm --network none --user 0 --entrypoint sh \
  -v lumbung_dashboard:/data -v ~/selfhost/lumbung-backups/dashboard:/b:ro lumbung-dashboard:latest -c '
  set -e
  src="$1"
  [ -f "$src.db" ] || { echo "no $src.db"; exit 1; }
  aside="/data/backups/before-restore-$(date -u +%Y%m%dT%H%M%SZ)"
  mkdir -p "$aside"
  for f in dashboard.db dashboard.db-wal dashboard.db-shm; do [ ! -e "/data/$f" ] || mv "/data/$f" "$aside/"; done
  cp "$src.db" /data/dashboard.db
  if [ -f "$src.auth-secret" ]; then
    mkdir -p /data/secrets
    [ ! -e /data/secrets/auth-secret ] || mv /data/secrets/auth-secret "$aside/"
    cp "$src.auth-secret" /data/secrets/auth-secret
  fi
  chown -R 65532:65532 /data
  chmod 700 /data/secrets "$aside"
  chmod 600 /data/dashboard.db /data/secrets/auth-secret' _ /b/nightly-dashboard-<time>
docker compose up -d --no-build --wait
```

## Alerts

- m4-alerts probes `https://lumbung.zakiego.com/health` and watches both containers.
- A failed deploy alerts through the webhook (email and WhatsApp).
- The nightly backup only writes its log. Check `~/selfhost/lumbung-backup.log` for `FAILED`.

## Releasing the SDK

`.github/workflows/release-client.yml` publishes `packages/client` to npm as `@lumbung/client` (public, MIT) when a `client-vX.Y.Z` tag is pushed. It runs the SDK build, typecheck and tests against a fresh `lumbungd` first, and fails if the tag doesn't match the version in `packages/client/package.json`. The workspace keeps `"private": true`, and the workflow drops it only for the publish, so nothing goes out from a laptop.

The repository is private, so npm doesn't attach provenance.

### One-time setup

1. On npmjs.com, create the free `lumbung` organization. It owns the `@lumbung` scope.
2. Create a granular access token with read and write on packages in the `lumbung` organization, and save it as the `NPM_TOKEN` repository secret. The first publish needs it because trusted publishing can only be set up on a package that already exists.
3. After the first release, open the package settings on npmjs.com and add a trusted publisher: GitHub Actions, repository `zakiego/lumbung`, workflow `release-client.yml`. Then delete the `NPM_TOKEN` secret and revoke the token. From then on npm signs in with the workflow's OIDC token.

### Each release

1. Bump `version` in `packages/client/package.json` and merge it to `main`.
2. Tag that commit and push the tag:

   ```bash
   git tag -a client-v0.1.1 -m "@lumbung/client 0.1.1"
   git push origin client-v0.1.1
   ```

3. Watch the run in the Actions tab, then check https://www.npmjs.com/package/@lumbung/client.

A version can't be published twice. To fix a bad release, bump the version and tag again.

`client-v0.1.0` went to GitHub Packages as `@zakiego/lumbung-client` before the switch to npm. That package can be deleted from the repository's Packages page.
