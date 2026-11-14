#!/bin/bash
# Install (or reinstall) the nightly Lumbung backup on the Docker host.
#   - copies ops/backup.sh to ~/selfhost/bin/lumbung-backup.sh, so the job
#     doesn't run a file a `git pull` is rewriting
#   - renders ops/com.zakiego.lumbung-backup.plist into ~/Library/LaunchAgents
#   - (re)loads it with launchctl and checks that launchd has it
#
#   ops/install-backup.sh           install; exits 1 when the job isn't loaded
#                                   afterwards
#   ops/install-backup.sh --check   change nothing; exit 0 when the installed
#                                   script and plist match the repo and the
#                                   job is loaded, 1 otherwise
#
# scripts/pre-deploy.sh runs --check on every deploy and reinstalls when it
# fails. Run the job once now with:
#   launchctl kickstart gui/$(id -u)/com.zakiego.lumbung-backup
#
# Env, so a test never touches the real agent: LUMBUNG_LAUNCHCTL (a fake
# launchctl), LUMBUNG_LAUNCHD_DOMAIN, LUMBUNG_BACKUP_LABEL,
# LUMBUNG_LAUNCH_AGENTS (folder for the plist), LUMBUNG_INSTALLED_BACKUP
# (where the script goes), LUMBUNG_SELFHOST (folder the job works in and
# logs to), LUMBUNG_BOOTSTRAP_DELAY (seconds between tries).
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
launchctl="${LUMBUNG_LAUNCHCTL:-launchctl}"
domain="${LUMBUNG_LAUNCHD_DOMAIN:-gui/$(id -u)}"
label="${LUMBUNG_BACKUP_LABEL:-com.zakiego.lumbung-backup}"
agents="${LUMBUNG_LAUNCH_AGENTS:-$HOME/Library/LaunchAgents}"
script="${LUMBUNG_INSTALLED_BACKUP:-$HOME/selfhost/bin/lumbung-backup.sh}"
selfhost="${LUMBUNG_SELFHOST:-$HOME/selfhost}"
delay="${LUMBUNG_BOOTSTRAP_DELAY:-2}"
plist="$agents/$label.plist"
ATTEMPTS=5

log() { echo "[install-backup] $*"; }

render() {
  sed -e "s|__LABEL__|$label|g" -e "s|__SCRIPT__|$script|g" -e "s|__SELFHOST__|$selfhost|g" \
    "$repo/ops/com.zakiego.lumbung-backup.plist"
}

loaded() { "$launchctl" print "$domain/$label" >/dev/null 2>&1; }

if [ "${1:-}" = --check ]; then
  cmp -s "$repo/ops/backup.sh" "$script" || { log "$script differs from ops/backup.sh"; exit 1; }
  render | cmp -s - "$plist" || { log "$plist differs from the repo's"; exit 1; }
  loaded || { log "$label isn't loaded in $domain"; exit 1; }
  exit 0
elif [ $# -gt 0 ]; then
  echo "usage: ops/install-backup.sh [--check]" >&2
  exit 2
fi

mkdir -p "$(dirname "$script")" "$agents" "$selfhost"
install -m 755 "$repo/ops/backup.sh" "$script"
render >"$plist.tmp"
plutil -lint "$plist.tmp" >/dev/null
mv "$plist.tmp" "$plist"

# bootout returns before launchd has removed the job, and a bootstrap while
# it's still there fails (often "Input/output error"). Wait until it's gone.
if loaded; then
  "$launchctl" bootout "$domain/$label" 2>/dev/null || true
  for ((i = 0; i < 20; i++)); do
    loaded || break
    sleep 0.5
  done
  loaded && log "$label is still loaded 10s after bootout, trying bootstrap anyway"
fi

for ((attempt = 1; attempt <= ATTEMPTS; attempt++)); do
  if "$launchctl" bootstrap "$domain" "$plist"; then
    break
  fi
  # A bootstrap can fail after loading the job; that counts as loaded.
  loaded && break
  log "bootstrap attempt $attempt of $ATTEMPTS failed"
  [ "$attempt" -lt "$ATTEMPTS" ] && sleep "$delay"
done

if ! loaded; then
  log "FAILED: $label is NOT loaded in $domain, so no nightly backup runs until this succeeds"
  exit 1
fi
log "installed $label: daily at 03:30, log $selfhost/lumbung-backup.log"
