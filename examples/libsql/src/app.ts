// The example app: the libSQL client and Drizzle's libsql driver, unchanged,
// on a Lumbung branch. Only the URL points at Lumbung.
import { createClient, type Client } from "@libsql/client"
import { eq, sql } from "drizzle-orm"
import { drizzle } from "drizzle-orm/libsql"

import * as schema from "./schema.ts"

// The branch is part of the URL, and the trailing slash matters: the client
// resolves its request paths against it.
export function libsqlURL(engineURL: string, database: string, branch: string) {
  return `${engineURL}/libsql/${database}/${branch}/`
}

export function connect(engineURL: string, token: string, database: string, branch: string): Client {
  return createClient({ url: libsqlURL(engineURL, database, branch), authToken: token })
}

export function orm(client: Client) {
  return drizzle(client, { schema })
}

export type DB = ReturnType<typeof orm>

// Moves money between two accounts in one interactive transaction: the
// balance check reads inside it, so nothing changes in between.
export async function transfer(db: DB, from: string, to: string, amount: number) {
  await db.transaction(async (tx) => {
    const [src] = await tx.select().from(schema.accounts).where(eq(schema.accounts.owner, from))
    if (!src || src.balance < amount) tx.rollback()
    await tx
      .update(schema.accounts)
      .set({ balance: sql`${schema.accounts.balance} - ${amount}` })
      .where(eq(schema.accounts.owner, from))
    await tx
      .update(schema.accounts)
      .set({ balance: sql`${schema.accounts.balance} + ${amount}` })
      .where(eq(schema.accounts.owner, to))
  })
}

// Run it: LUMBUNG_URL=... LUMBUNG_TOKEN=... LUMBUNG_DATABASE=app node src/app.ts
if (import.meta.url === `file://${process.argv[1]}`) {
  const { LUMBUNG_URL, LUMBUNG_TOKEN, LUMBUNG_DATABASE } = process.env
  if (!LUMBUNG_URL || !LUMBUNG_TOKEN || !LUMBUNG_DATABASE) throw new Error("set LUMBUNG_URL, LUMBUNG_TOKEN and LUMBUNG_DATABASE")
  const client = connect(LUMBUNG_URL, LUMBUNG_TOKEN, LUMBUNG_DATABASE, "main")
  console.log((await client.execute("SELECT sqlite_version() AS version")).rows)
  client.close()
}
