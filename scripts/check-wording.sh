#!/usr/bin/env bash
# Enforces the wording rule (PLAN.md section 2): Lumbung is described on its
# own terms. Scans repo files (tracked and untracked, not ignored), file
# names, commit messages, and the PR title and body when CI passes them in
# PR_TITLE and PR_BODY. Exits 1 and prints each hit when something breaks it.
#
# Which commits are checked:
#   WORDING_COMMITS=<rev-list range>   that range (CI passes the pushed or PR range)
#   otherwise                          commits not on the upstream branch yet,
#                                      or the whole history when there's no upstream
# Checking only new commits keeps one bad commit that already landed from
# failing every later run, since history is never rewritten.
#
# Usage: bash scripts/check-wording.sh [repo-root]
set -euo pipefail

root="${1:-$(git rev-parse --show-toplevel)}"
cd "$root"
if ! git rev-parse --git-dir >/dev/null 2>&1; then
  echo "wording: not a git repository: $root" >&2
  exit 2
fi

# A hosted product we never name. Built from character classes so this file
# doesn't spell it out. Matched as a whole word, any case.
name_re='[Dd][1]'
# Comparison framing. Only the files that state the rule may quote it.
phrase_re='clone of|alternative to|drop-in replacement for'
rule_files=(PLAN.md AGENTS.md scripts/check-wording.sh scripts/check-wording.test.sh)

# Files whose content is hashes or generated ids, where a short token like the
# name above can appear by chance. Without glob magic, * in a pathspec also
# matches /, so these cover any depth.
hash_files=(':!*pnpm-lock.yaml' ':!*go.sum')

fail=0
report() {
  echo "wording: $1"
  fail=1
}

# Runs a search command and stores its output in $hits. grep and git grep exit
# 0 on a match, 1 on no match, and 2 or more on an error, which must fail the
# check instead of reading as "no match".
search() {
  local rc=0
  hits="$("$@")" || rc=$?
  if [ "$rc" -gt 1 ]; then
    echo "wording: search failed (exit $rc): $*" >&2
    exit 2
  fi
  [ "$rc" -eq 0 ]
}

if search git grep --untracked -n -I -i -w -E "$name_re" -- . "${hash_files[@]}"; then
  report "banned product name in files:"
  echo "$hits"
fi

rule_excludes=()
for f in "${rule_files[@]}"; do rule_excludes+=(":!$f"); done
if search git grep --untracked -n -I -i -E "$phrase_re" -- . "${rule_excludes[@]}"; then
  report "comparison wording in files:"
  echo "$hits"
fi

names="$(git ls-files; git ls-files --others --exclude-standard)"
if search grep -i -w -E "$name_re" <<<"$names"; then
  report "banned product name in file names:"
  echo "$hits"
fi

# Commit messages.
commits=()
if [ -n "${WORDING_COMMITS:-}" ]; then
  while IFS= read -r sha; do commits+=("$sha"); done < <(git rev-list "$WORDING_COMMITS")
elif git rev-parse --verify -q '@{upstream}' >/dev/null 2>&1; then
  while IFS= read -r sha; do commits+=("$sha"); done < <(git rev-list '@{upstream}..HEAD')
elif git rev-parse --verify -q HEAD >/dev/null; then
  while IFS= read -r sha; do commits+=("$sha"); done < <(git rev-list HEAD)
fi
for sha in ${commits[@]+"${commits[@]}"}; do
  msg="$(git log -1 --format=%B "$sha")"
  if search grep -i -w -E "$name_re" <<<"$msg" || search grep -i -E "$phrase_re" <<<"$msg"; then
    report "commit $sha breaks the wording rule: $(git log -1 --format=%s "$sha")"
  fi
done

for var in PR_TITLE PR_BODY; do
  text="${!var:-}"
  [ -z "$text" ] && continue
  if search grep -i -w -E "$name_re" <<<"$text" || search grep -i -E "$phrase_re" <<<"$text"; then
    report "$var breaks the wording rule"
  fi
done

if [ "$fail" -ne 0 ]; then
  echo "wording: see PLAN.md section 2"
  exit 1
fi
echo "wording: ok (${#commits[@]} commits checked)"
