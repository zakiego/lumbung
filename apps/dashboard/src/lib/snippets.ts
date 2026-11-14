// Connect snippets for one database branch or one bucket. Tokens never go in
// a snippet: code reads them from the environment.

export type Snippet = { value: string; label: string; code: string }

/** The libSQL URL of a branch. The trailing slash matters: without it the client drops the branch. */
export function libsqlUrl(url: string, db: string, branch: string) {
  return `${url}/libsql/${db}/${branch}/`
}

export function s3Endpoint(url: string) {
  return `${url}/v1/s3`
}

// Lumbung accepts any region; this is the one S3 clients expect.
export const S3_REGION = "us-east-1"

/** `branch` is the branch to show; `pinBranch` is false when it's the default, so SDK, CLI and REST snippets leave it out. */
export function dbSnippets({ url, db, branch, pinBranch }: { url: string; db: string; branch: string; pinBranch: boolean }): Snippet[] {
  const sdkBranch = pinBranch ? `\n  branch: "${branch}",` : ""
  const cliBranch = pinBranch ? ` --branch ${branch}` : ""
  const path = pinBranch ? `/v1/databases/${db}/branches/${branch}/query` : `/v1/databases/${db}/query`
  const libsql = libsqlUrl(url, db, branch)
  return [
    {
      value: "sdk",
      label: "SDK",
      code: `import { createClient } from "@lumbung/client"

const db = createClient({
  url: "${url}",
  token: process.env.LUMBUNG_TOKEN!,
  database: "${db}",${sdkBranch}
})

const { rows } = await db.query("SELECT 1")`,
    },
    {
      value: "libsql",
      label: "libSQL",
      code: `import { createClient } from "@libsql/client"

const client = createClient({
  url: "${libsql}",
  authToken: process.env.LUMBUNG_TOKEN,
})

const rs = await client.execute("SELECT 1")`,
    },
    {
      value: "drizzle",
      label: "Drizzle",
      code: `import { createClient } from "@libsql/client"
import { drizzle } from "drizzle-orm/libsql"

const client = createClient({
  url: "${libsql}",
  authToken: process.env.LUMBUNG_TOKEN,
})

export const db = drizzle(client)`,
    },
    {
      value: "cli",
      label: "CLI",
      code: `echo "$LUMBUNG_TOKEN" | lumbung login --url ${url}
lumbung query "SELECT 1" --db ${db}${cliBranch}`,
    },
    {
      value: "curl",
      label: "curl",
      code: `curl -s ${url}${path} \\
  -H "Authorization: Bearer $LUMBUNG_TOKEN" \\
  -H "Content-Type: application/json" \\
  -d '{"sql": "SELECT 1"}'`,
    },
    {
      value: "mcp",
      label: "MCP",
      code: `claude mcp add --transport http lumbung ${url}/mcp \\
  --header "Authorization: Bearer $LUMBUNG_TOKEN"`,
    },
  ]
}

export function bucketSnippets({ url, bucket }: { url: string; bucket: string }): Snippet[] {
  const endpoint = s3Endpoint(url)
  return [
    {
      value: "sdk",
      label: "SDK",
      code: `import { createClient } from "@lumbung/client"

const client = createClient({
  url: "${url}",
  token: process.env.LUMBUNG_TOKEN!,
})
const bucket = client.bucket("${bucket}")

await bucket.put("hello.txt", "hello", { contentType: "text/plain" })
const file = await bucket.get("hello.txt")`,
    },
    {
      value: "aws",
      label: "AWS SDK",
      code: `import { PutObjectCommand, S3Client } from "@aws-sdk/client-s3"

const s3 = new S3Client({
  endpoint: "${endpoint}",
  forcePathStyle: true,
  region: "${S3_REGION}",
  credentials: {
    accessKeyId: process.env.LUMBUNG_ACCESS_KEY_ID!,
    secretAccessKey: process.env.LUMBUNG_SECRET_ACCESS_KEY!,
  },
})

await s3.send(new PutObjectCommand({ Bucket: "${bucket}", Key: "hello.txt", Body: "hello" }))`,
    },
    {
      value: "boto3",
      label: "boto3",
      code: `import os
import boto3
from botocore.config import Config

s3 = boto3.client(
    "s3",
    endpoint_url="${endpoint}",
    aws_access_key_id=os.environ["LUMBUNG_ACCESS_KEY_ID"],
    aws_secret_access_key=os.environ["LUMBUNG_SECRET_ACCESS_KEY"],
    region_name="${S3_REGION}",
    config=Config(s3={"addressing_style": "path"}),
)

s3.put_object(Bucket="${bucket}", Key="hello.txt", Body=b"hello")`,
    },
    {
      value: "rclone",
      label: "rclone",
      code: `# ~/.config/rclone/rclone.conf
[lumbung]
type = s3
provider = Other
endpoint = ${endpoint}
access_key_id = LMB...
secret_access_key = ...
force_path_style = true

# then: rclone ls lumbung:${bucket}`,
    },
    {
      value: "cli",
      label: "CLI",
      code: `echo "$LUMBUNG_TOKEN" | lumbung login --url ${url}
echo hello > hello.txt
lumbung object put hello.txt ${bucket}/
lumbung object ls ${bucket}`,
    },
    {
      value: "curl",
      label: "curl",
      code: `curl -X PUT "${url}/v1/buckets/${bucket}/object?key=hello.txt" \\
  -H "Authorization: Bearer $LUMBUNG_TOKEN" \\
  -H "Content-Type: text/plain" \\
  --data-binary 'hello'`,
    },
  ]
}
