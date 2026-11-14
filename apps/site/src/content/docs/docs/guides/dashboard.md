---
title: Use the dashboard
description: Set up the owner, work in workspaces, invite people into the ones they need, and sign in with a password or a passkey.
---

The dashboard runs next to the engine and talks to it through the same REST API you use. With the repository's `compose.yaml` it answers at `http://localhost:4871/app`.

## Set up the owner

The first visit opens the setup page. It asks for the engine's service token, which proves you can reach the server, so nobody who finds a fresh dashboard first can claim it:

<!-- docs-test: skip needs Docker Compose -->
```sh
docker compose exec engine lumbungd service-token
```

Paste it, then pick your name, email and a password of at least 10 characters. You're signed in as the owner. Setup closes once one user exists.

## Workspaces

Everything in the dashboard happens inside a [workspace](/docs/concepts/workspaces/). Its name is at the top of the sidebar and in the address, `/app/w/<workspace>/...`, so a link opens the same workspace for whoever you send it to, and two tabs can show two workspaces. Addresses from before workspaces still work: they open the same page in the default workspace.

Click the name to switch. An owner also finds **New workspace** there. Under **Workspace** in the sidebar, an admin of the workspace has its tokens, its audit log, its trash and its settings:

- **Settings** shows what the workspace uses against its limits. An owner can rename it, change the limits (an empty field means no limit) and delete it.
- A deleted workspace stops working at once, with its databases, buckets and tokens. An owner restores it under **Instance > Settings** until the trash retention passes (see [Deleting and restoring](/docs/concepts/workspaces/#deleting-and-restoring)).
- An owner moves a database or a bucket to another workspace from its **Settings** tab. Every token that reached it stops working, so make new ones in the new workspace.

**Tokens** and **Audit log** have two tabs for an owner: the workspace's, and the instance's. Instance tokens belong to no workspace and reach all of them, and the instance log holds every workspace's entries.

## Invite people

Under **Members**, an owner chooses **Invite**, enters an email, and picks what the person gets: every workspace as an owner, or a role in each workspace chosen. Send the link you get. It works once and expires after 7 days. Only its hash is stored, so if it's lost, use **New link** on the invite, which also stops the old link.

The person opens the link, picks a name and a password, and is signed in. There's no public sign-up.

## Roles

An owner has the whole instance. Everyone else has a role in each workspace they were given, and can have different roles in different workspaces:

| Role | Can |
|---|---|
| Owner | Everything in every workspace, and what belongs to the instance: workspaces and their limits, moving a database or a bucket between them, members, invites, instance tokens and settings |
| Admin | In that workspace: create, rename, delete and restore databases, create and delete buckets and change their settings, tokens, the audit log and the trash |
| Member | In that workspace: change data and schema, branches and snapshots, and upload and delete objects |
| Read-only | In that workspace: browse databases and data, run read-only SQL, and browse and download objects |

A workspace someone has no role in doesn't exist for them: it isn't in their switcher, and its address shows the same page as a workspace that was never made. Someone with no workspace at all can sign in and sees that they have none yet.

The engine checks the role too, not only the dashboard. Each request the dashboard makes for someone who isn't an owner names their workspace and carries their level, and the engine treats it as a token of that level for that workspace. So a member's request can't create a database even if the dashboard asked for it.

Read-only SQL from the dashboard runs on a read-only connection, the same as a token with `read` access. At least one owner always remains: the last owner can't be removed or demoted. Changing someone's access signs them out, so it applies at once.

A token is a way in that doesn't need the dashboard, so it doesn't outlive what its maker may do. When someone is removed, or stops being an admin of a workspace, the tokens they made there through the dashboard are revoked. The same goes for instance tokens made by someone who stops being an owner. Make a token as an owner if it should outlast the people around it.

Deleting a workspace keeps people's roles in it, so a restore gives them back. Until then **Members** shows such a role marked as deleted.

Upgrading from before workspaces: members and read-only people keep that role, in the default workspace. Two things a member could do now take an admin of the workspace: creating, renaming and deleting databases, and reading the audit log. Make someone an admin of the workspace under **Members** if they need those.

## Buckets

Buckets are in the sidebar under Buckets. A bucket page lists its objects with folders rolled up at `/`; click a folder to go in, and the breadcrumb to go back. Upload sends files into the folder you're in, streamed through the dashboard to the engine without being held in memory. Files over 16 MiB go up as a multipart upload, one 16 MiB part per request, so a large file never meets a request timeout and a failed upload is aborted with its parts. The [object limits](/docs/concepts/limits/#object-storage) apply. Downloads always save as files: the dashboard never shows an object's content in the page.

An admin's bucket settings turn public read on or off, set the CORS origins for browser uploads, and delete the bucket once it is empty.

## Connect

Each database and each bucket has a **Connect** tab. It lists the values a client needs, each with a copy button, and has ready-to-paste code for that database or bucket.

- A database's tab shows the engine URL, the libSQL URL of the branch picked in the header, the MCP URL, and code for the SDK, `@libsql/client`, Drizzle, the CLI, curl and MCP.
- A bucket's tab shows the engine URL, the S3 endpoint, the region and the bucket, a link to create an access key (for admins), and code for the SDK, the AWS SDK, boto3, rclone, the CLI and curl.

The code reads the token from `LUMBUNG_TOKEN` (and S3 keys from `LUMBUNG_ACCESS_KEY_ID` and `LUMBUNG_SECRET_ACCESS_KEY`), so no secret ever appears on the page. The URLs come from `LUMBUNG_PUBLIC_ENGINE_URL`, described in [Dashboard configuration](/docs/reference/dashboard-configuration/).

## Passkeys

Under **Account** (in the menu at the bottom of the sidebar), add a passkey, then use **Sign in with a passkey** next time. Passkeys are bound to the dashboard's address, `LUMBUNG_DASHBOARD_URL`, so set that to the address people really open before anyone adds one. They don't work on the extra addresses in `LUMBUNG_DASHBOARD_EXTRA_ORIGINS`: there the login page and **Account** say where passkeys work, and you sign in with your password.

## Overview and search

A workspace's home page lists its databases with their size, and their requests and errors over the last 1, 6, 24 or 48 hours. Requests and errors count each database's default branch. Admins also see recent activity from the audit log.

Press <kbd>⌘K</kbd> (<kbd>Ctrl+K</kbd> on Windows and Linux) to jump to a database, another workspace or a page, or to switch the theme.

## Who did what

The dashboard calls the engine with its service token and names the signed-in person in `X-Lumbung-Actor`, so the audit log shows `dashboard:<email>` for every change made from the dashboard. Each entry is in the log of the workspace it happened in.

## Settings

The dashboard reads environment variables, listed in [Dashboard configuration](/docs/reference/dashboard-configuration/). The ones you usually set are `LUMBUNG_DASHBOARD_URL` (the address people open) and, behind a proxy, `LUMBUNG_CLIENT_IP_HEADER` and `LUMBUNG_TRUSTED_PROXIES`.

To keep the dashboard usable when the public address is down (the proxy, the tunnel or the network), list the addresses you open it at on the server itself in `LUMBUNG_DASHBOARD_EXTRA_ORIGINS`, for example `http://127.0.0.1:4871,http://localhost:4871`. Password sign-in and everything after it work there. Each address has its own sign-in cookie, so you sign in once per address, and passkeys only work on `LUMBUNG_DASHBOARD_URL`. A request counts as sent to an extra address when its `Host` header matches it, so a proxy in front of the public address must pass the original `Host` through.

Users, sessions and who has which role in which workspace live in the dashboard's own `dashboard.db`. Workspaces, databases, buckets and tokens live in the engine. Back up both.
