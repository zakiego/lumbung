// The example app: Drizzle on a Lumbung branch.
import { drizzle } from "@lumbung/client/drizzle"
import { desc, eq } from "drizzle-orm"

import * as schema from "./schema.ts"

export function connect(url: string, token: string, database: string, branch?: string) {
  return drizzle({ url, token, database, branch }, { schema })
}

export type DB = ReturnType<typeof connect>

export async function addUserWithPost(db: DB, email: string, name: string, title: string) {
  const [user] = await db.insert(schema.users).values({ email, name }).returning()
  await db.insert(schema.posts).values({ authorId: user!.id, title })
  return user!
}

export async function latestPosts(db: DB) {
  return db
    .select({ title: schema.posts.title, author: schema.users.name })
    .from(schema.posts)
    .innerJoin(schema.users, eq(schema.posts.authorId, schema.users.id))
    .orderBy(desc(schema.posts.id))
    .all()
}

// Run it: LUMBUNG_URL=... LUMBUNG_TOKEN=... LUMBUNG_DATABASE=app node --experimental-strip-types src/app.ts
if (import.meta.url === `file://${process.argv[1]}`) {
  const { LUMBUNG_URL, LUMBUNG_TOKEN, LUMBUNG_DATABASE } = process.env
  if (!LUMBUNG_URL || !LUMBUNG_TOKEN || !LUMBUNG_DATABASE) throw new Error("set LUMBUNG_URL, LUMBUNG_TOKEN and LUMBUNG_DATABASE")
  const db = connect(LUMBUNG_URL, LUMBUNG_TOKEN, LUMBUNG_DATABASE)
  await addUserWithPost(db, `ana-${Date.now()}@example.com`, "Ana", "Hello from Drizzle")
  console.log(await latestPosts(db))
}
