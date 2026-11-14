import { passkeyClient } from "@better-auth/passkey/client"
import { createAuthClient } from "better-auth/react"
import { AUTH_BASE } from "./paths"

function create() {
  return createAuthClient({ basePath: AUTH_BASE, plugins: [passkeyClient()] })
}

let client: ReturnType<typeof create> | undefined

// Created on first use in the browser, so server rendering never builds one without a base URL.
export function authClient() {
  client ??= create()
  return client
}

export function unwrap<T>(res: { data: T; error: null } | { data: null; error: { message?: string; status: number } }): T {
  if (res.error) throw new Error(res.error.message || `Request failed (${res.error.status}).`)
  return res.data as T
}
