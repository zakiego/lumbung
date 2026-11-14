import { expect, test } from "vitest"
import { bucketSnippets, dbSnippets, libsqlUrl } from "./snippets"

const url = "https://db.example.com"

test("the libSQL URL names the branch and keeps the trailing slash", () => {
  expect(libsqlUrl(url, "app", "pr-42")).toBe("https://db.example.com/libsql/app/pr-42/")
})

test("database snippets name this database, and the branch only when it isn't the default", () => {
  const onDefault = Object.fromEntries(dbSnippets({ url, db: "app", branch: "main", pinBranch: false }).map((s) => [s.value, s.code]))
  expect(onDefault.sdk).toContain('database: "app"')
  expect(onDefault.sdk).not.toContain("branch:")
  expect(onDefault.cli).not.toContain("--branch")
  expect(onDefault.curl).toContain("/v1/databases/app/query")
  // libSQL needs a branch in the URL even on the default branch.
  expect(onDefault.libsql).toContain('url: "https://db.example.com/libsql/app/main/"')
  expect(onDefault.drizzle).toContain("drizzle(client)")

  const onBranch = Object.fromEntries(dbSnippets({ url, db: "app", branch: "pr-42", pinBranch: true }).map((s) => [s.value, s.code]))
  expect(onBranch.sdk).toContain('branch: "pr-42"')
  expect(onBranch.cli).toContain("--db app --branch pr-42")
  expect(onBranch.curl).toContain("/v1/databases/app/branches/pr-42/query")
  expect(onBranch.libsql).toContain("/libsql/app/pr-42/")
})

test("bucket snippets point at this bucket and the S3 endpoint, with path-style addressing", () => {
  const code = Object.fromEntries(bucketSnippets({ url, bucket: "photos" }).map((s) => [s.value, s.code]))
  expect(code.sdk).toContain('client.bucket("photos")')
  expect(code.aws).toContain('endpoint: "https://db.example.com/v1/s3"')
  expect(code.aws).toContain("forcePathStyle: true")
  expect(code.aws).toContain('Bucket: "photos"')
  expect(code.boto3).toContain('"addressing_style": "path"')
  expect(code.rclone).toContain("force_path_style = true")
  expect(code.cli).toContain("lumbung object ls photos")
  expect(code.curl).toContain("/v1/buckets/photos/object?key=hello.txt")
})

test("no snippet carries a token, only the variable that holds it", () => {
  const all = [...dbSnippets({ url, db: "app", branch: "main", pinBranch: false }), ...bucketSnippets({ url, bucket: "photos" })]
  for (const s of all) expect(s.code).not.toMatch(/lmb_[A-Za-z0-9]/)
})
