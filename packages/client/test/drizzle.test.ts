import { randomUUID } from "node:crypto"
import { eq, sql } from "drizzle-orm"
import { blob, integer, real, sqliteTable, text } from "drizzle-orm/sqlite-core"
import { expect, inject, test } from "vitest"

import { createClient } from "../src/index.ts"
import { drizzle } from "../src/drizzle.ts"

const url = inject("url")
const token = inject("token")

const users = sqliteTable("users", {
  id: integer("id").primaryKey({ autoIncrement: true }),
  email: text("email").notNull().unique(),
  score: real("score"),
  avatar: blob("avatar", { mode: "buffer" }),
})

test("drizzle on a Lumbung branch", async () => {
  const name = `drizzle-${randomUUID().slice(0, 8)}`
  const admin = createClient({ url, token })
  await admin.databases.create(name)
  const db = drizzle({ url, token, database: name }, { schema: { users } })

  await db.run(sql`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL UNIQUE, score REAL, avatar BLOB)`)
  const inserted = await db.insert(users).values({ email: "a@example.com", score: 1.5 }).returning()
  expect(inserted).toEqual([{ id: 1, email: "a@example.com", score: 1.5, avatar: null }])

  await db.insert(users).values([{ email: "b@example.com" }, { email: "c@example.com" }])
  const one = await db.select().from(users).where(eq(users.email, "b@example.com")).get()
  expect(one?.id).toBe(2)
  const missing = await db.select().from(users).where(eq(users.email, "nobody")).get()
  expect(missing).toBeUndefined()

  await db.update(users).set({ score: 9 }).where(eq(users.id, 3))
  const all = await db.select({ email: users.email, score: users.score }).from(users).all()
  expect(all).toHaveLength(3)
  expect(all[2]).toEqual({ email: "c@example.com", score: 9 })

  const counts = await db.select({ n: sql<number>`count(*)` }).from(users).values()
  expect(counts).toEqual([[3]])

  // db.batch is one Lumbung batch: one transaction.
  await expect(
    db.batch([db.insert(users).values({ email: "d@example.com" }), db.insert(users).values({ email: "a@example.com" })]),
  ).rejects.toMatchObject({ code: "constraint_violation" })
  expect(await db.select().from(users).all()).toHaveLength(3)
  const [, rows] = await db.batch([db.insert(users).values({ email: "e@example.com" }), db.select().from(users)])
  expect(rows).toHaveLength(4)

  // db.transaction() can't work over HTTP; it fails with a clear error.
  await expect(db.transaction(async (tx) => tx.select().from(users))).rejects.toMatchObject({ cause: { code: "unsupported" } })
})
