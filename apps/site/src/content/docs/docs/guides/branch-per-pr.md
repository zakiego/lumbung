---
title: A branch per pull request
description: Give every pull request its own copy of the database, run its migrations there, and delete it when the pull request closes.
sidebar:
  order: 6
---

Each pull request gets a branch: a full copy of the default branch, with the pull request's migrations applied. Preview deployments point at it. The branch expires on its own if nobody cleans up.

## The commands

Create the branch and apply the pull request's migrations to it:

```sh
mkdir -p migrations
printf 'ALTER TABLE users ADD COLUMN avatar_url TEXT;\n' > migrations/0001_avatar.sql

lumbung branch create pr-42 --db app --expires-in-days 14
lumbung migrate apply --db app --branch pr-42 --dir migrations
lumbung branch diff pr-42 --db app
```

Give the preview deployment a token that reaches only that branch:

```sh
lumbung token create --name preview-pr-42 --db app --branch pr-42 --access write --expires-in-days 14
```

When the pull request merges, apply the same migrations to the default branch and delete the preview branch:

```sh
lumbung migrate apply --db app --dir migrations
lumbung branch delete pr-42 --db app --yes
```

Branches never merge data back. Merging the pull request means running its migrations on the default branch, which is what the last step does.

## In GitHub Actions

Store a token with admin access to the database as a repository secret. Branch and token management need admin; queries need only write.

```yaml
# .github/workflows/preview-db.yml
name: Preview database
on:
  pull_request:
    types: [opened, synchronize, reopened, closed]

jobs:
  branch:
    runs-on: ubuntu-latest
    env:
      LUMBUNG_URL: ${{ vars.LUMBUNG_URL }}
      LUMBUNG_TOKEN: ${{ secrets.LUMBUNG_ADMIN_TOKEN }}
      BRANCH: pr-${{ github.event.number }}
    steps:
      - uses: actions/checkout@v7
      - run: go install github.com/zakiego/lumbung/apps/engine/cmd/lumbung@latest
      - if: github.event.action != 'closed'
        run: |
          lumbung branch create "$BRANCH" --db app --expires-in-days 14 || true
          lumbung migrate apply --db app --branch "$BRANCH" --dir migrations
      - if: github.event.action == 'closed'
        run: lumbung branch delete "$BRANCH" --db app --yes
```

`branch create ... || true` keeps later pushes working once the branch exists. To start fresh on every push instead, run `lumbung branch reset "$BRANCH" --db app --yes` before applying.
