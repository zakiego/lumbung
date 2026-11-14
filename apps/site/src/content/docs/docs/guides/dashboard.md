---
title: Use the dashboard
description: Set up the owner, invite your team with a role, and sign in with a password or a passkey.
---

The dashboard runs next to the engine and talks to it through the same REST API you use. With the repository's `compose.yaml` it answers at `http://localhost:4871/app`.

## Set up the owner

The first visit opens the setup page. It asks for the engine's service token, which proves you can reach the server, so nobody who finds a fresh dashboard first can claim it:

<!-- docs-test: skip needs Docker Compose -->
```sh
docker compose exec engine lumbungd service-token
```

Paste it, then pick your name, email and a password of at least 10 characters. You're signed in as the owner. Setup closes once one user exists.

## Invite people

Under **Members**, choose **Invite**, enter an email and a role, and send the link you get. The link works once and expires after 7 days. Only its hash is stored, so if it's lost, use **New link** on the invite, which also stops the old link.

The person opens the link, picks a name and a password, and is signed in. There's no public sign-up.

## Roles

| Role | Can |
|---|---|
| Owner | Everything, including tokens, members, invites and instance settings |
| Member | Create and change databases, branches, snapshots, data and schema, and read the audit log |
| Read-only | Browse databases and data, and run read-only SQL |

Read-only SQL from the dashboard runs on a read-only connection, the same as a token with `read` access. At least one owner always remains: the last owner can't be removed or demoted. A role change signs that person out, so it applies at once.

## Passkeys

Under **Account** (in the menu at the bottom of the sidebar), add a passkey, then use **Sign in with a passkey** next time. Passkeys are bound to the dashboard's address, `LUMBUNG_DASHBOARD_URL`, so set that to the address people really open before anyone adds one.

## Overview and search

The home page lists every database with its size, and its requests and errors over the last 1, 6, 24 or 48 hours. Requests and errors count each database's default branch. People who can write also see recent activity from the audit log.

Press <kbd>⌘K</kbd> (<kbd>Ctrl+K</kbd> on Windows and Linux) to jump to a database or page, or to switch the theme.

## Who did what

The dashboard calls the engine with its service token and names the signed-in person in `X-Lumbung-Actor`, so the audit log shows `dashboard:<email>` for every change made from the dashboard.

## Settings

The dashboard reads environment variables, listed in [Dashboard configuration](/docs/reference/dashboard-configuration/). The ones you usually set are `LUMBUNG_DASHBOARD_URL` (the address people open) and, behind a proxy, `LUMBUNG_CLIENT_IP_HEADER` and `LUMBUNG_TRUSTED_PROXIES`.

Users and sessions live in the dashboard's own `dashboard.db`. Databases, branches and tokens live in the engine.
