#!/bin/bash
# Install (or reinstall) the nightly Lumbung backup on the Docker host.
#   - copies ops/backup.sh to ~/selfhost/bin/lumbung-backup.sh, so the job
#     doesn't run a file a `git pull` is rewriting
#   - renders ops/com.zakiego.lumbung-backup.plist into ~/Library/LaunchAgents
#   - (re)loads it with launchctl
# Rerun after changing either file. scripts/pre-deploy.sh reruns it when
# backup.sh changes. Run the job once now with:
#   launchctl kickstart gui/$(id -u)/com.zakiego.lumbung-backup
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
label=com.zakiego.lumbung-backup
plist="$HOME/Library/LaunchAgents/$label.plist"
domain="gui/$(id -u)"

mkdir -p "$HOME/selfhost/bin" "$HOME/Library/LaunchAgents"
install -m 755 "$repo/ops/backup.sh" "$HOME/selfhost/bin/lumbung-backup.sh"
sed "s|__HOME__|$HOME|g" "$repo/ops/$label.plist" >"$plist"
plutil -lint "$plist" >/dev/null

launchctl bootout "$domain/$label" 2>/dev/null || true
launchctl bootstrap "$domain" "$plist"
echo "installed $label: daily at 03:30, log ~/selfhost/lumbung-backup.log"
