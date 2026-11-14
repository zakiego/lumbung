import { APP_BASE } from "./paths"

/** The dashboard's own download route for an object in a workspace. */
export function objectURL(ws: string, bucket: string, key: string): string {
  return `${APP_BASE}/api/objects?${new URLSearchParams({ ws, bucket, key })}`
}

/** The anonymous S3 URL of an object, or null when the key can't travel in a URL path. */
export function publicObjectURL(s3Endpoint: string, bucket: string, key: string): string | null {
  const segments = key.split("/")
  // Browsers resolve . and .. in a path, even percent-encoded, so the URL would name another key.
  if (segments.some((s) => s === "." || s === "..")) return null
  return `${s3Endpoint}/${bucket}/${segments.map(encodeURIComponent).join("/")}`
}
