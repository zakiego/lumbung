// Dashboard settings, read from the environment once. Every setting has a
// default that works next to `pnpm dev:engine`, so dev needs no env file.
import { randomBytes } from "node:crypto"
import { BlockList, isIP } from "node:net"
import { chmodSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { settingDefault } from "./settings"

export interface DashboardConfig {
  /** Where the engine's REST API answers, e.g. http://engine:4870 in compose. */
  engineUrl: string
  /** The public origin people open, without /app. Better Auth and passkeys bind to it. */
  publicUrl: string
  /** Folder for dashboard.db (users, sessions, invites) and generated secrets. */
  dataDir: string
  /** The engine URL people's apps use, shown in connect snippets. Defaults to publicUrl. */
  publicEngineUrl: string
  /** LUMBUNG_CLIENT_IP_HEADER, like X-Forwarded-For or CF-Connecting-IP. Empty uses the connection's address. Same as the engine. */
  clientIpHeader: string
  /** LUMBUNG_TRUSTED_PROXIES: networks whose connections may set that header. Same default as the engine. */
  trustedProxies: BlockList
}

export const DEFAULT_TRUSTED_PROXIES = settingDefault("LUMBUNG_TRUSTED_PROXIES")

/** Comma-separated IPs or CIDR networks, parsed the way the engine's ParseNetworks does. */
export function parseNetworks(list: string): BlockList {
  const out = new BlockList()
  for (const raw of list.split(",")) {
    const part = raw.trim()
    if (!part) continue
    const [ip, bits, extra] = part.split("/")
    const family = isIP(ip ?? "")
    const max = family === 4 ? 32 : 128
    const prefix = bits === undefined ? max : /^\d{1,3}$/.test(bits) ? Number(bits) : Number.NaN
    if (!family || extra !== undefined || !(prefix >= 0 && prefix <= max)) throw new Error(`${JSON.stringify(part)} isn't an IP or a CIDR network`)
    out.addSubnet(ip!, prefix, family === 4 ? "ipv4" : "ipv6")
  }
  return out
}

function trustedProxiesFrom(value: string | undefined): BlockList {
  try {
    return parseNetworks(value?.trim() ? value : DEFAULT_TRUSTED_PROXIES)
  } catch (e) {
    throw new Error(`LUMBUNG_TRUSTED_PROXIES: ${e instanceof Error ? e.message : String(e)}`)
  }
}

let cached: DashboardConfig | undefined

export function config(): DashboardConfig {
  if (cached) return cached
  const env = process.env
  const publicUrl = (env.LUMBUNG_DASHBOARD_URL || settingDefault("LUMBUNG_DASHBOARD_URL")).replace(/\/+$/, "")
  const origin = new URL(publicUrl).origin
  cached = {
    engineUrl: (env.LUMBUNG_ENGINE_URL || settingDefault("LUMBUNG_ENGINE_URL")).replace(/\/+$/, ""),
    publicUrl: origin,
    dataDir: resolve(env.LUMBUNG_DASHBOARD_DATA_DIR || settingDefault("LUMBUNG_DASHBOARD_DATA_DIR")),
    publicEngineUrl: (env.LUMBUNG_PUBLIC_ENGINE_URL || origin).replace(/\/+$/, ""),
    clientIpHeader: (env.LUMBUNG_CLIENT_IP_HEADER || "").trim(),
    trustedProxies: trustedProxiesFrom(env.LUMBUNG_TRUSTED_PROXIES),
  }
  return cached
}

/** Only for tests. */
export function resetConfig() {
  cached = undefined
  serviceToken = undefined
  authSecret = undefined
}

let serviceToken: string | undefined

/**
 * The engine's service token: LUMBUNG_SERVICE_TOKEN, or the file named by
 * LUMBUNG_SERVICE_TOKEN_FILE (the engine writes one to secrets/service-token
 * in its data folder). Read on first use, so the dashboard can start before
 * the engine has generated it.
 */
export function engineToken(): string {
  if (serviceToken) return serviceToken
  const env = process.env
  let token = env.LUMBUNG_SERVICE_TOKEN?.trim()
  if (!token) {
    const file = env.LUMBUNG_SERVICE_TOKEN_FILE || settingDefault("LUMBUNG_SERVICE_TOKEN_FILE")
    try {
      token = readFileSync(resolve(file), "utf8").trim()
    } catch {
      throw new Error(`No service token. Set LUMBUNG_SERVICE_TOKEN, or LUMBUNG_SERVICE_TOKEN_FILE to the engine's secrets/service-token.`)
    }
  }
  if (!/^lmb_[^_\s]+_\S+$/.test(token)) throw new Error("The service token isn't a Lumbung token (lmb_<id>_<secret>).")
  serviceToken = token
  return token
}

let authSecret: string | undefined

/** LUMBUNG_AUTH_SECRET, or one generated on first start and kept in the data folder. */
export function authSecretValue(): string {
  if (authSecret) return authSecret
  const fromEnv = process.env.LUMBUNG_AUTH_SECRET?.trim()
  if (fromEnv) {
    if (fromEnv.length < 32) throw new Error("LUMBUNG_AUTH_SECRET must be at least 32 characters.")
    authSecret = fromEnv
    return fromEnv
  }
  const dir = join(config().dataDir, "secrets")
  const file = join(dir, "auth-secret")
  if (!existsSync(file)) {
    mkdirSync(dir, { recursive: true, mode: 0o700 })
    writeFileSync(file, randomBytes(32).toString("base64url"), { mode: 0o600, flag: "wx" })
  }
  chmodSync(file, 0o600)
  authSecret = readFileSync(file, "utf8").trim()
  return authSecret
}
