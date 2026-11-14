// Tokens people made through the dashboard, and who made each. A token is a
// way into a workspace that doesn't need the dashboard, so it must not
// outlive what its maker may do: when someone is removed, or stops being an
// admin of a workspace (or an owner, for an instance token), the tokens they
// made there are revoked.
import { LumbungError } from "@lumbung/client"
import { type Actor, engineFor } from "./engine"
import { grantsOf } from "./access"
import { getAuth } from "./auth"
import { getDb } from "./db"

/** The workspace id recorded for an instance token, which belongs to none. */
const INSTANCE = ""

export function recordMinted(tokenId: string, workspaceId: string | null, userId: string) {
  getDb()
    .prepare(`INSERT OR REPLACE INTO minted_token (token_id, workspace_id, user_id, created_at) VALUES (?, ?, ?, ?)`)
    .run(tokenId, workspaceId ?? INSTANCE, userId, Date.now())
}

export function forgetMinted(tokenId: string) {
  getDb().prepare(`DELETE FROM minted_token WHERE token_id = ?`).run(tokenId)
}

type Row = { token_id: string; workspace_id: string; user_id: string }

/** Whether the person who made a token could still make it today. */
function stillTheirs(row: Row, owners: Set<string>, users: Set<string>): boolean {
  if (!users.has(row.user_id)) return false
  if (owners.has(row.user_id)) return true
  if (row.workspace_id === INSTANCE) return false
  return grantsOf(row.user_id).some((g) => g.workspace === row.workspace_id && g.role === "admin")
}

/**
 * Revokes every recorded token whose maker could no longer make it, and
 * returns how many. Run after access changes, after a member is removed and
 * after a workspace is restored. A token that can't be revoked now (its
 * workspace is in the trash, where it doesn't work either) stays recorded
 * and is revoked when the workspace comes back.
 */
export async function revokeOutlived(actor: Actor): Promise<number> {
  await getAuth()
  const db = getDb()
  const rows = db.prepare(`SELECT token_id, workspace_id, user_id FROM minted_token`).all() as Row[]
  if (rows.length === 0) return 0
  const people = db.prepare(`SELECT id, role FROM "user"`).all() as { id: string; role: string | null }[]
  const users = new Set(people.map((p) => p.id))
  const owners = new Set(people.filter((p) => p.role === "owner").map((p) => p.id))
  const engine = engineFor(actor)
  let revoked = 0
  for (const row of rows) {
    if (stillTheirs(row, owners, users)) continue
    try {
      if (row.workspace_id === INSTANCE) await engine.instance.tokens.revoke(row.token_id)
      else await engine.workspace(row.workspace_id).tokens.revoke(row.token_id)
      revoked++
    } catch (e) {
      // Already revoked or expired and removed: nothing left to do.
      if (!(e instanceof LumbungError && e.code === "token_not_found")) continue
    }
    forgetMinted(row.token_id)
  }
  return revoked
}
