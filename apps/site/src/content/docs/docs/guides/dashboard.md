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
| Owner | Everything, including tokens, members, invites, instance settings, and creating, changing and deleting buckets |
| Member | Create and change databases, branches, snapshots, data and schema, upload and delete objects, and read the audit log |
| Read-only | Browse databases and data, run read-only SQL, and browse and download objects |

Read-only SQL from the dashboard runs on a read-only connection, the same as a token with `read` access. At least one owner always remains: the last owner can't be removed or demoted. A role change signs that person out, so it applies at once.

## Buckets

Buckets are in the sidebar under Buckets. A bucket page lists its objects with folders rolled up at `/`; click a folder to go in, and the breadcrumb to go back. Upload sends files into the folder you're in, streamed through the dashboard to the engine without being held in memory. The [object limits](/docs/concepts/limits/#object-storage) apply, and an upload must finish within the dashboard server's request timeout (Node's default is 5 minutes), so send very large files with an S3 client or `lumbung object put`, which upload in parts or without that limit. Downloads always save as files: the dashboard never shows an object's content in the page.

An owner's bucket settings turn public read on or off, set the CORS origins for browser uploads, show the S3 endpoint, and delete the bucket once it is empty.

## Passkeys

Under **Account** (in the menu at the bottom of the sidebar), add a passkey, then use **Sign in with a passkey** next time. Passkeys are bound to the dashboard's address, `LUMBUNG_DASHBOARD_URL`, so set that to the address people really open before anyone adds one.

## Who did what

The dashboard calls the engine with its service token and names the signed-in person in `X-Lumbung-Actor`, so the audit log shows `dashboard:<email>` for every change made from the dashboard.

## Settings

The dashboard reads environment variables, listed in [Dashboard configuration](/docs/reference/dashboard-configuration/). The ones you usually set are `LUMBUNG_DASHBOARD_URL` (the address people open) and, behind a proxy, `LUMBUNG_CLIENT_IP_HEADER` and `LUMBUNG_TRUSTED_PROXIES`.

Users and sessions live in the dashboard's own `dashboard.db`. Databases, branches and tokens live in the engine.
