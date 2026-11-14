#!/usr/bin/env bash
# Starts a dev server on a port that is stable per checkout.
#
#   bash scripts/dev.sh engine   lumbungd
#   bash scripts/dev.sh dashboard  TanStack Start dev server
#   bash scripts/dev.sh site     Astro dev server
#
# The main checkout uses the documented ports (engine 4870, site 4872). A
# linked worktree gets its own block of three ports in 4900-4997 (engine,
# dashboard, site), derived from its path, so two worktrees can run at once.
# The chosen URL is printed first.
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
git_dir="$(git rev-parse --path-format=absolute --git-dir)"
common_dir="$(git rev-parse --path-format=absolute --git-common-dir)"

if [ "$git_dir" = "$common_dir" ]; then
  base=4870
else
  # 33 blocks of three ports: 4900, 4903, ... 4996.
  slot=$(( $(printf '%s' "$root" | cksum | cut -d' ' -f1) % 33 ))
  base=$(( 4900 + slot * 3 ))
fi

case "${1:-}" in
engine)
  port=$base
  echo "lumbungd: http://127.0.0.1:$port"
  cd "$root/apps/engine"
  # Build, then exec the binary, so a stop signal reaches lumbungd itself
  # (go run doesn't forward it).
  go build -o bin/lumbungd ./cmd/lumbungd
  LUMBUNG_ADDR="127.0.0.1:$port" exec ./bin/lumbungd
  ;;
dashboard)
  port=$((base + 1))
  echo "dashboard: http://localhost:$port"
  cd "$root/apps/dashboard"
  # Point at this checkout's engine and its service token, and bind auth to this port.
  LUMBUNG_ENGINE_URL="http://127.0.0.1:$base" \
    LUMBUNG_PUBLIC_ENGINE_URL="http://127.0.0.1:$base" \
    LUMBUNG_SERVICE_TOKEN_FILE="$root/apps/engine/data/secrets/service-token" \
    LUMBUNG_DASHBOARD_URL="http://localhost:$port" \
    exec ./node_modules/.bin/vite dev --port "$port" --strictPort
  ;;
site)
  port=$((base + 2))
  echo "site: http://localhost:$port"
  cd "$root/apps/site"
  exec ./node_modules/.bin/astro dev --port "$port"
  ;;
*)
  echo "usage: bash scripts/dev.sh engine|dashboard|site" >&2
  exit 2
  ;;
esac
