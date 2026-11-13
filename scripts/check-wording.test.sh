#!/usr/bin/env bash
# Tests scripts/check-wording.sh against throwaway repos. The banned name is
# assembled at run time so it never appears in this file.
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/check-wording.sh"
name="$(printf '%s%s' D 1)"
lower="$(printf '%s%s' d 1)"
pass=0

new_repo() {
  dir="$(mktemp -d)"
  git -C "$dir" init -q
  git -C "$dir" config user.email test@example.com
  git -C "$dir" config user.name test
  echo "$dir"
}

expect() {
  local want="$1" desc="$2" dir="$3"
  shift 3
  local got=0
  env "$@" bash "$script" "$dir" >/dev/null 2>&1 || got=1
  if [ "$got" -ne "$want" ]; then
    echo "FAIL: $desc (exit $got, want $want)"
    bash "$script" "$dir" || true
    exit 1
  fi
  echo "ok: $desc"
  pass=$((pass + 1))
  rm -rf "$dir"
}

d="$(new_repo)"; echo "plain text about SQLite" >"$d/a.md"
expect 0 "clean repo passes" "$d"

d="$(new_repo)"; echo "works like Cloudflare $name" >"$d/a.md"
expect 1 "name in an untracked file" "$d"

d="$(new_repo)"; echo "a $lower database" >"$d/a.ts"; git -C "$d" add a.ts
expect 1 "lowercase name in a tracked file" "$d"

d="$(new_repo)"; echo "id1 d12 x${name}y ${lower}_x" >"$d/a.md"
expect 0 "name inside longer words is not flagged" "$d"

d="$(new_repo)"; echo "sha512-ab+$name/cd" >"$d/pnpm-lock.yaml"; mkdir -p "$d/apps/engine"; echo "h1:$name+" >"$d/apps/engine/go.sum"
expect 0 "hash files are skipped" "$d"

d="$(new_repo)"; echo "Lumbung is a clone of something" >"$d/README.md"
expect 1 "comparison phrase in a doc" "$d"

d="$(new_repo)"; echo 'No "clone of", no "alternative to"' >"$d/PLAN.md"
expect 0 "rule files may quote the phrases" "$d"

d="$(new_repo)"; mkdir -p "$d/x"; touch "$d/x/${lower}.txt"
expect 1 "name in a file name" "$d"

d="$(new_repo)"; echo ok >"$d/a.md"; git -C "$d" add a.md; git -C "$d" commit -q -m "feat: like $name"
expect 1 "name in a commit message" "$d"

d="$(new_repo)"; echo ok >"$d/a.md"
expect 1 "name in the PR title" "$d" PR_TITLE="feat: $name style api"

d="$(new_repo)"; echo ok >"$d/a.md"
expect 1 "phrase in the PR body" "$d" PR_BODY="an alternative to it"

echo "check-wording tests: $pass passed"
