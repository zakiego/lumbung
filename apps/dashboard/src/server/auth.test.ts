import { describe, expect, test } from "vitest"
import { blockedAuthPath } from "./auth"

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
