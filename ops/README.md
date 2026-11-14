# ops: our deployment

How we run Lumbung at https://lumbung.zakiego.com on the Mac mini (the `zaki-selfhost` skill). None of this is part of the product. The generic setup is `compose.yaml` and the Self-hosting docs.

## What runs where

| Thing | Where |
|---|---|
| Checkout | `~/selfhost/lumbung` (compose project `lumbung`) |
| Compose files | `compose.yaml` + `ops/compose.selfhost.yaml` (ports, `mem_limit`, logs, public URLs, proxy header) |
| Engine | `127.0.0.1:4805` (container 4870), 1 GB, volume `lumbung_data` |
| Dashboard | `127.0.0.1:4806` (container 4871), 512 MB, volume `lumbung_dashboard` |
| Site | Cloudflare Pages, `SITE_ORIGIN` in `apps/edge/wrangler.jsonc` |
| Edge Worker | `apps/edge`, route `lumbung.zakiego.com/*` |
| Nightly backup | launchd `com.zakiego.lumbung-backup`, 03:30, log `~/selfhost/lumbung-backup.log` |

Requests go Worker first. `/app`, `/v1`, `/mcp` and `/health` pass to the zone origin (the tunnel), everything else to Pages. The tunnel splits the app paths by port, with the path rule above the host rule:

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

1. Clone and deploy. The first `pre-deploy.sh` skips the backups (no volumes yet) and installs the nightly backup agent.

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

`deploy.sh` runs `scripts/pre-deploy.sh` (backs up `meta.db` and `dashboard.db`), then `scripts/deploy-up.sh` (builds engine, then dashboard, swaps, rolls back to `:prev` when health fails).

Going back one image after a healthy deploy:

```bash
docker tag lumbung-engine:prev lumbung-engine:latest
docker tag lumbung-dashboard:prev lumbung-dashboard:latest
docker compose up -d --no-build --wait
```

If the new engine migrated `meta.db`, the old one answers 503 on `/health`. Put back the pre-deploy copy with the engine stopped (the image's entrypoint is `lumbungd`, so the arguments start at `restore`):

```bash
docker compose stop engine
docker compose run --rm --no-deps engine restore --meta-only --from /data/backups/pre-deploy
docker compose up -d --no-build --wait
```

## Backups

| Kind | Where | Kept |
|---|---|---|
| Pre-deploy `meta.db` | `lumbung_data:/data/backups/pre-deploy/<time>-meta` | 5 |
| Pre-deploy `dashboard.db` | `lumbung_dashboard:/data/backups/pre-deploy-<time>.db` | 5 |
| Nightly, full engine | `lumbung_data:/data/backups/nightly/<time>` and `~/selfhost/lumbung-backups/engine/<time>` | 7 each |
| Nightly `dashboard.db` | `lumbung_dashboard:/data/backups/nightly-dashboard-<time>.db` and `~/selfhost/lumbung-backups/dashboard/` | 7 each |

The host copies hold the service token (folder mode 700). They survive a lost volume, not a lost disk.

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

## Alerts

- m4-alerts probes `https://lumbung.zakiego.com/health` and watches both containers.
- A failed deploy alerts through the webhook (email and WhatsApp).
- The nightly backup only writes its log. Check `~/selfhost/lumbung-backup.log` for `FAILED`.
