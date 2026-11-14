# 0004: ui-sdk for the dashboard and the landing page

Date: 2026-11-14. Status: accepted.

## Context

PLAN 3 and 10 named shadcn for the dashboard, and the milestone 7 prototypes were built with it. When milestone 8 started, Zaki asked for ui-sdk instead.

ui-sdk ships behaviors (keyboard navigation, focus, dismissal, popups and the ARIA they need) as unstyled React components, plus the core they're built from. It renders no look of its own, so the look stays ours.

## Decision

- The dashboard and the landing page's React islands use `@ui-sdk/react` for every interactive part: buttons, fields, selects, dialogs, menus, tabs, tooltips, toasts, pagination and the rest.
- Our components in `apps/dashboard/src/components/ui` wrap ui-sdk parts and style them with the existing tokens through `data-slot` and state attributes. `web.css` isn't used, because the dashboard already has its look (Linear-style tokens, one indigo accent).
- shadcn, its `components.json` and its Base UI dependency are removed from the dashboard. The two are never mixed in one app.
- A part ui-sdk doesn't ship is built from `@ui-sdk/core` behaviors, not hand-rolled with `role=` attributes.
- Plain layout pieces with no behavior (cards, badges, tables, skeletons) stay as styled HTML.

## Consequences

- ui-sdk is a beta (`0.1.0-beta.1` when this was written). Versions are pinned exactly, and an upgrade is its own commit.
- The Branch map overview and the shell are rebuilt on ui-sdk with the same layout and tokens Zaki picked.
- Every interface gets the agent test that ui-sdk's docs describe, next to the component tests PLAN 13 asks for.
