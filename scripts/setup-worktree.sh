#!/usr/bin/env bash
# Prepare a fresh git worktree for development.
# T3 runs it in the worktree on create (see t3.json); it also works by hand.
# Lumbung has a pnpm workspace and a Go module (apps/engine), so it installs both.
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
# T3 sets T3CODE_PROJECT_ROOT to the main checkout; fall back to git outside T3.
main_root="${T3CODE_PROJECT_ROOT:-$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")}"
cd "$root"

# Copy gitignored local env and secret files from the main checkout, if missing here.
if [ "$main_root" != "$root" ] && [ -d "$main_root" ]; then
  while IFS= read -r file; do
    rel="${file#"$main_root"/}"
    if [ ! -e "$root/$rel" ]; then
      mkdir -p "$(dirname "$root/$rel")"
      cp "$file" "$root/$rel"
      echo "copied $rel"
    fi
  done < <(find "$main_root" \( -name node_modules -o -name .git -o -name .venv -o -name data \) -prune -o \
    -type f \( -name '.env*' -o -name '.dev.vars*' \) -print)
fi

# Install dependencies. CI=true and a closed stdin keep installers from
# waiting on a prompt nobody can answer.
export CI=true
if [ -f pnpm-lock.yaml ]; then
  pnpm install --frozen-lockfile --prefer-offline < /dev/null
fi

# Go modules live in the shared module cache, so this is cheap after the first
# worktree. It also fetches the toolchain go.mod asks for.
if [ -f apps/engine/go.mod ]; then
  (cd apps/engine && go mod download < /dev/null)
fi
