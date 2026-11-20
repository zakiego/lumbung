# Security policy

## Reporting a vulnerability

Please don't open a public issue for a security problem. Report it privately through GitHub instead: open the repo's **Security** tab and click **Report a vulnerability**.

Include what you found, how to reproduce it, and which part it affects (engine, dashboard, CLI, SDK). A proof of concept against your own local instance helps. Don't test against instances you don't run.

You'll get a reply within 7 days. Once a fix ships, the advisory is published with credit to you unless you'd rather stay anonymous.

## Supported versions

Lumbung has no stable release yet. Fixes land on `main` only.

## Scope

In scope: anything that lets one token, workspace or database reach data it wasn't granted, bypasses dashboard sign-in or roles, escapes the SQL sandbox to touch other files, or exposes secrets. The isolation rules are described in [Security model](apps/site/src/content/docs/docs/concepts/security-model.md).

Out of scope: denial of service by sending lots of traffic to an instance, and issues that need an instance admin token to begin with.
