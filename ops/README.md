# ops: our deployment

How we run Lumbung at https://lumbung.zakiego.com on the Mac mini (the `zaki-selfhost` skill). None of this is part of the product. The generic setup is `compose.yaml` and the Self-hosting docs.

## What runs where

| Thing | Where |
|---|---|
| Checkout | `~/selfhost/lumbung` (compose project `lumbung`) |
| Compose files | `compose.yaml` + `ops/compose.selfhost.yaml` (ports, `mem_limit`, logs, public URLs, proxy header) |
| Engine | `127.0.0.1:4805` (container 4870), 1 GB, volume `lumbung_data` |
| Dashboard | `127.0.0.1:4806` (container 4871), 512 MB, volume `lumbung_dashboard`. Stops when idle (Scale to zero, below). Also signs in at http://127.0.0.1:4806/app and http://localhost:4806/app on the Mac mini (password only, no passkeys), for when the tunnel is down |
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

## Scale to zero

The dashboard runs only while someone uses it. The engine has no label and stays on.

The `zaki-selfhost` edge (Caddy with the Sablier plugin, compose project `edge` on `127.0.0.1:4811`) sits between the tunnel and the dashboard. On the first `/app` request it starts `lumbung-dashboard-1` and holds the request until the container is healthy (about 1.5 s), then proxies to `127.0.0.1:4806`. Fifteen minutes after the last request, Sablier stops the container. The dashboard opts in with the `sablier.enable` label in `ops/compose.selfhost.yaml`.

Once the edge is live, the dashboard's tunnel rule points at the edge instead of at 4806. The engine rule doesn't change:

```yaml
  # Lumbung dashboard through the zaki-selfhost edge (starts it on demand), above the engine rule
  - hostname: lumbung.zakiego.com
    path: ^/app(/|$)
    service: http://127.0.0.1:4811
```

What changes with the dashboard stopped:

- A deploy starts it, and the edge stops it again 30 minutes later. The deploy needs nothing from the edge.
- The nightly backup copies `dashboard.db` from its volume with a one-off container (`ops/backup.sh`), the same way `deploy-up.sh` does before a swap.
- Signing in on the Mac mini at http://127.0.0.1:4806/app needs the container running first, since that address skips the edge: `docker start lumbung-dashboard-1`. The edge stops it 30 minutes later.
- Rolling back: point the tunnel rule at 4806 again and restart the tunnel, stop the edge (`cd ~/selfhost/edge && docker compose stop`) so nothing stops the dashboard any more, then `docker start lumbung-dashboard-1`. Removing the label and deploying takes it out of the edge's hands for good.

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
docker run --rm --network none --user 65532 -v lumbung_data:/data alpine:3.24.4 rm -rf /data/backups/pre-rollback/<time>
```

## Upgrading to workspaces

Workspaces (PLAN 17, ADR 0008) take `meta.db` from version 5 to 6 and give the dashboard roles per workspace. Nothing here has been run against the live instance. The steps were run against a local copy shaped like it (`pnpm rehearse:upgrade`, below), and that run passes.

What the deploy does by itself, with no step from us:

- Migration 6 makes a workspace called `default` and puts everything in it: databases `testing` and `palui`, buckets `photos` and `palui-files`, and every token that isn't an instance token.
- Every token keeps working, on the same URLs. mcporter's instance admin token and the dashboard's service token stay instance tokens. Palui's grants token still reaches its database and its bucket.
- Public links to `photos` keep working: short URLs mean the default workspace.
- The first time the dashboard is opened, the people who were Member or Read-only get that role in `default`. Owners stay owners.

So after step 2 the instance behaves as before. Steps 3 to 6 give Palui its own workspace, which is the point of the change.

### 1. Before

1. Run the restore drill from the checkout as it is now, before pulling anything. It must print PASS.

   ```bash
   cd ~/selfhost/lumbung && ./ops/restore-drill.sh
   ```

2. Take one more backup by hand and check it:

   ```bash
   launchctl kickstart gui/$(id -u)/com.zakiego.lumbung-backup
   tail -n 20 ~/selfhost/lumbung-backup.log
   ```

3. Keep a way back that later deploys can't push out. Every deploy retags `:prev` and keeps only 5 pre-deploy copies of `meta.db`, so after a few more deploys neither is from before workspaces any more.

   ```bash
   docker tag lumbung-engine:latest lumbung-engine:pre-workspaces
   docker tag lumbung-dashboard:latest lumbung-dashboard:pre-workspaces
   docker compose exec -T engine lumbungd backup --to /data/backups/pre-workspaces --meta-only
   ```

4. Write down what exists now, to compare after:

   ```bash
   lumbung db list
   lumbung bucket list
   lumbung token list
   ```

### 2. Deploy

Every push to `main` deploys, so merge the stack as one push: merge each PR into the one below it, from the top down (#35 into #34's branch, and so on down to #32 into #31's), then merge #31 into `main`. Or deploy by hand:

```bash
~/.agents/skills/zaki-selfhost/scripts/deploy.sh lumbung
```

`deploy-up.sh` copies `meta.db` and `dashboard.db` right before the swap, as on every deploy. Migration 6 runs when the new engine starts and takes well under a second on an instance this size.

Build the CLI from the new checkout before the next steps. The old one has no `workspace` commands, no `move` and no `--workspace`.

```bash
pnpm --filter @lumbung/engine build      # apps/engine/bin/lumbung
```

Check:

```bash
curl -s https://lumbung.zakiego.com/health          # "meta_version": 6
lumbung workspace list                               # default, with 2 databases and 2 buckets
lumbung db list && lumbung bucket list               # the same as before
lumbung token list                                   # the tokens of the default workspace
lumbung token list --instance                        # mcporter's token and the service token are listed here now
```

Then the things that must not have noticed:

- Palui: ask it to read something from its database and list its files.
- mcporter: `~/.mcporter/mcporter-call.sh lumbung.list_databases` shows `testing` and `palui`.
- A public `photos` link opens.
- The dashboard at https://lumbung.zakiego.com/app opens on `/app/w/default`, with the same databases and buckets.

If the new engine is unhealthy, `deploy-up.sh` rolls back by itself, and `rollback-meta.sh` puts the version 5 `meta.db` back from the copy taken before the swap.

### 3. Make Palui's workspace

With the CLI logged in as the instance admin (the same token mcporter uses):

```bash
lumbung workspace create palui
```

It gets the default limits: 20 databases, 20 buckets, 2 GiB of databases, 5 GiB of objects. `default` has none, so check that what moves fits, and raise a limit if it doesn't. A move that doesn't fit is refused.

```bash
lumbung db list                                      # the size of palui: under 2 GiB?
lumbung bucket list                                  # the size of palui-files: under 5 GiB?
lumbung workspace update palui --max-database-bytes 10GiB --max-object-bytes 20GiB
```

### 4. Mint Palui's new token first

A workspace token made before the move already reaches whatever is in the workspace later, so Palui's outage is only the time between step 5 and step 6.

```bash
rm -f ~/selfhost/palui.token
(umask 077; lumbung token create --name palui --access admin --workspace palui > ~/selfhost/palui.token)
```

`admin` is what lets Palui create and delete its own databases and buckets. It can do nothing outside `palui`: no other workspace exists as far as that token can tell.

Check the token works before anything is revoked. The header goes through standard input, so the token is never on a command line. It must print `palui`:

```bash
test -s ~/selfhost/palui.token
curl -s https://lumbung.zakiego.com/v1/workspaces -H @- <<<"Authorization: Bearer $(cat ~/selfhost/palui.token)" | jq -r '.data[].name'
```

Don't check it with an ad-hoc mcporter call: mcporter reuses the saved `lumbung` server's header for any URL that matches it, so it would test the instance token instead.

### 5. Move Palui's database and bucket

Each move revokes the tokens that reached what moved, and that can't be undone: moving it back doesn't bring a token back. Palui's old grants token reaches both, so it stops working at the first move. Do the two moves and step 6 back to back.

```bash
lumbung db move palui --to palui
lumbung bucket move palui-files --to palui
lumbung db list --workspace palui                    # palui
lumbung bucket list --workspace palui                # palui-files
```

If the second move is refused (a limit, most likely), the old token is already gone and the only way is forward: raise the limit as in step 3, run the move again, and go on. Nothing is lost meanwhile, and the instance admin token can always mint another token.

`testing` and `photos` stay in `default`. `photos` has public links in use, and a public link outside the default workspace needs the workspace in its URL, so leave it.

### 6. Give Palui the token

In Palui, open the Lumbung MCP connector and replace the bearer token with the content of `~/selfhost/palui.token`. The MCP URL stays `https://lumbung.zakiego.com/mcp`: the token says which workspace. Nothing else in Palui changes.

Check from Palui: read a row, list its files, and have it create a database and delete it again (it couldn't before). One more look from outside, which must print `palui` and nothing else, then remove the file and clear the clipboard if the token was copied:

```bash
curl -s https://lumbung.zakiego.com/v1/databases -H @- <<<"Authorization: Bearer $(cat ~/selfhost/palui.token)" | jq -r '.data[].name'
rm ~/selfhost/palui.token
pbcopy </dev/null
```

### 7. After

- mcporter keeps its instance token. It acts in `default` on the saved URL. To work inside Palui's workspace, call `https://lumbung.zakiego.com/v1/workspaces/palui/mcp`, or save that as a second server. An instance token reaches every workspace, Palui's included.
- Dashboard: under **Members**, give each person the workspaces they need. Anyone who was a Member and should still create databases or read the audit log needs **Admin** in that workspace.
- Publish `@lumbung/client` 0.2.0 (Releasing the SDK, below) only now. It needs an engine with workspaces.

  ```bash
  git tag -a client-v0.2.0 -m "@lumbung/client 0.2.0"
  git push origin client-v0.2.0
  ```

- Run the restore drill once more. It walks every workspace now, and fails when a workspace, a database or a bucket that is in the backup isn't in the restored copy.

### Going back to before workspaces

Possible while the two `:pre-workspaces` images and the `pre-workspaces` copy of `meta.db` from step 1 exist. It costs more than a token, so read this before doing it:

- `meta.db` goes back to the moment of step 1. Every database, branch, snapshot and bucket made since is forgotten, and the engine deletes their files when it starts. That includes databases Palui made for itself. Objects uploaded since are gone, and objects deleted since are listed again without their bytes coming back.
- Tokens go back too: the workspace token from step 4 doesn't exist, and Palui's old grants token works again. A token revoked since step 1 works again.
- `rollback-meta.sh` takes a full backup of every branch first (`pre-rollback/<time>`), which is how to get databases back (Getting data back, above). It doesn't cover objects: for those, restore the last nightly backup instead.
- The old dashboard reads the same `dashboard.db`. The upgrade added three tables and one column and changed nothing the old dashboard reads. But the old dashboard knows one role for the whole instance, and anyone invited or changed since the upgrade is Read-only to it, on everything. Someone who was given one workspace, or none, can then read `testing` and `photos`. Remove those people under **Members** before going back, or right after.

```bash
docker tag lumbung-engine:pre-workspaces lumbung-engine:latest
docker tag lumbung-dashboard:pre-workspaces lumbung-dashboard:latest
./ops/rollback-meta.sh --from /data/backups/pre-workspaces/<time>-meta
docker compose up -d --no-build --wait
```

Once the upgrade has settled, remove the way back:

```bash
docker rmi lumbung-engine:pre-workspaces lumbung-dashboard:pre-workspaces
docker run --rm --network none --user 65532 -v lumbung_data:/data alpine:3.24.4 rm -rf /data/backups/pre-workspaces
```

### The rehearsal

`pnpm rehearse:upgrade` (`scripts/upgrade-rehearsal.sh`) runs the upgrade on one machine, against nothing live. It builds the engine from before workspaces (`meta.db` version 5) and fills it like our instance: databases `testing` and `palui`, a public bucket `photos`, a bucket `palui-files`, an instance admin token, a grants token, a database token, a bucket token and the service token. Then it starts the new engine on the same data folder and checks:

- every old token still works, and the public link still opens
- each move revokes the tokens that reached what moved, and only those
- the new workspace token reaches Palui's things on the same URLs, MCP included, and nothing outside its workspace exists for it
- a backup of the upgraded instance restores with every workspace in place, and the restore check fails when one is taken away

```bash
REHEARSAL_OLD_REF=<the commit running in production> pnpm rehearse:upgrade
```

What it doesn't cover: going back, the `lumbung` CLI commands above (it goes through curl), a move refused by a limit, and the dashboard's own upgrade (people's roles moving into the default workspace). The last one is covered by `apps/dashboard/src/server/access.test.ts`, and by `pnpm e2e` for a fresh dashboard.

## Palui's agent

Palui's agent keeps its own rows and files here through MCP, in a workspace of its own. Its token is an admin of that workspace: it makes the databases and buckets it needs, and nothing outside the workspace exists for it (ADR 0008). Before workspaces it had one token for one database and one bucket (ADR 0007).

1. Its token is a workspace token for the workspace `palui` (Upgrading to workspaces, above). To make another, open the `palui` workspace in the dashboard: **Tokens**, **New token**, keep **Whole workspace**, pick **Admin**, then create. For a narrower one, pick a database or a bucket instead.
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
docker run --rm --network none --user 0 \
  -v lumbung_dashboard:/data -v ~/selfhost/lumbung-backups/dashboard:/b:ro alpine:3.24.4 sh -c '
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
   git tag -a client-v0.2.0 -m "@lumbung/client 0.2.0"
   git push origin client-v0.2.0
   ```

3. Watch the run in the Actions tab, then check https://www.npmjs.com/package/@lumbung/client.

A version can't be published twice. To fix a bad release, bump the version and tag again.

`client-v0.1.0` went to GitHub Packages as `@zakiego/lumbung-client` before the switch to npm. That package can be deleted from the repository's Packages page.
