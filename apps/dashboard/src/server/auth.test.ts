import { afterEach, describe, expect, test, vi } from "vitest"
import { authHeaders, blockedAuthPath, clientIp } from "./auth"
import { config, parseNetworks, resetConfig } from "./config"

describe("blockedAuthPath", () => {
  const req = (path: string) => new Request(`http://localhost:4871${path}`)

  test.each([
    "/app/api/auth/get-session",
    "/app/api/auth/sign-in/email",
    "/app/api/auth/sign-out",
    "/app/api/auth/change-password",
    "/app/api/auth/passkey/generate-authenticate-options",
    "/app/api/auth/passkey/verify-registration",
    "/app/api/auth/get-session/",
    "/app/api/auth/SIGN-IN/email",
  ])("lets %s through", (path) => {
    expect(blockedAuthPath(req(path))).toBe(false)
  })

  test.each([
    "/app/api/auth/sign-up/email",
    "/app/api/auth/sign-up%2Femail",
    "/app/api/auth//sign-up/email",
    "/app/api/auth/update-user",
    "/app/api/auth/delete-user",
    "/app/api/auth/list-sessions",
    "/app/api/auth/passkey/anything-else",
    "/app/api/auth",
    "/app/api/authx/get-session",
    "/api/auth/get-session",
    "/app/api/auth/%E0%A4%A",
  ])("blocks %s", (path) => {
    expect(blockedAuthPath(req(path))).toBe(true)
  })
})

describe("clientIp", () => {
  const req = (headers: Record<string, string>, ip: string) => Object.assign(new Request("http://localhost:4871/app/api/auth/sign-in/email", { headers }), { ip })
  const settings = (clientIpHeader: string, proxies = "127.0.0.1,::1,10.0.0.0/8") => ({ clientIpHeader, trustedProxies: parseNetworks(proxies) })
  const xff = settings("X-Forwarded-For")

  test("with no header set, it's the socket address and proxy headers are ignored", () => {
    const r = req({ "x-forwarded-for": "1.1.1.1", "cf-connecting-ip": "2.2.2.2" }, "127.0.0.1")
    expect(clientIp(r, settings(""))).toBe("127.0.0.1")
  })

  test("a header from an untrusted socket is ignored, however it's rotated", () => {
    expect(clientIp(req({ "x-forwarded-for": "4.4.4.4" }, "203.0.113.9"), xff)).toBe("203.0.113.9")
    expect(clientIp(req({ "x-forwarded-for": "5.5.5.5" }, "203.0.113.9"), xff)).toBe("203.0.113.9")
  })

  test("X-Forwarded-For is read from the right, skipping trusted proxies", () => {
    expect(clientIp(req({ "x-forwarded-for": "6.6.6.6, 198.51.100.7, 10.1.2.3" }, "127.0.0.1"), xff)).toBe("198.51.100.7")
    expect(clientIp(req({ "x-forwarded-for": "198.51.100.7" }, "::ffff:127.0.0.1"), xff)).toBe("198.51.100.7")
    expect(clientIp(req({ "x-forwarded-for": "10.0.0.1, 127.0.0.1" }, "127.0.0.1"), xff)).toBe("127.0.0.1")
  })

  test("a missing or malformed header falls back to the socket address", () => {
    expect(clientIp(req({}, "127.0.0.1"), xff)).toBe("127.0.0.1")
    expect(clientIp(req({ "x-forwarded-for": "1.1.1.1, nonsense" }, "127.0.0.1"), xff)).toBe("127.0.0.1")
    expect(clientIp(req({ "x-forwarded-for": "1.1.1.1:443" }, "127.0.0.1"), xff)).toBe("127.0.0.1")
  })

  test("CF-Connecting-IP is trusted from a trusted socket only", () => {
    const cf = settings("CF-Connecting-IP")
    expect(clientIp(req({ "cf-connecting-ip": "2001:db8::1" }, "::1"), cf)).toBe("2001:db8::1")
    expect(clientIp(req({ "cf-connecting-ip": "1.1.1.1" }, "203.0.113.9"), cf)).toBe("203.0.113.9")
  })

  test("authHeaders overwrites a client-sent copy of the internal header", () => {
    const r = req({ "x-lumbung-client-ip": "7.7.7.7" }, "203.0.113.9")
    expect(authHeaders(r).get("x-lumbung-client-ip")).toBe("203.0.113.9")
  })
})

describe("client IP settings", () => {
  afterEach(() => {
    vi.unstubAllEnvs()
    resetConfig()
  })

  test("default to the socket address with loopback trusted, like the engine", () => {
    vi.stubEnv("LUMBUNG_CLIENT_IP_HEADER", "")
    vi.stubEnv("LUMBUNG_TRUSTED_PROXIES", "")
    resetConfig()
    expect(config().clientIpHeader).toBe("")
    expect(config().trustedProxies.check("127.0.0.1", "ipv4")).toBe(true)
    expect(config().trustedProxies.check("::1", "ipv6")).toBe(true)
    expect(config().trustedProxies.check("10.0.0.1", "ipv4")).toBe(false)
  })

  test("reject a network that isn't an IP or a CIDR", () => {
    for (const bad of ["10.0.0.0/33", "nope", "10.0.0.1/8/1", "::1/129"]) {
      vi.stubEnv("LUMBUNG_TRUSTED_PROXIES", bad)
      resetConfig()
      expect(() => config()).toThrow(/LUMBUNG_TRUSTED_PROXIES/)
    }
    expect(parseNetworks(" 192.168.0.0/16 , fd00::/8 ").check("192.168.4.4", "ipv4")).toBe(true)
  })
})
