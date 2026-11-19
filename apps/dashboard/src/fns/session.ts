// Session, first-run setup and invite acceptance.
import { createServerFn } from "@tanstack/react-start"
import { getCookie, getRequest } from "@tanstack/react-start/server"
import { z } from "zod"
import { LAST_WORKSPACE_COOKIE } from "@/lib/workspace"
import { visibleWorkspaces } from "@/server/access"
import { authHeaders, getAuth, passkeysAvailable } from "@/server/auth"
import { config, engineToken } from "@/server/config"
import { requireSameOrigin, getViewer } from "@/server/session"
import { UserError, acceptInvite as accept, createOwner, findInvite, sameSecret, userCount } from "@/server/users"

const email = z.string().trim().email().max(200)
const name = z.string().trim().min(1).max(100)
const password = z.string().min(1).max(200)
const token = z.string().min(10).max(200)

export const getSession = createServerFn({ method: "GET" }).handler(async () => {
  const [viewer, users] = await Promise.all([getViewer(), userCount()])
  // The workspaces the person may use ride along, each with their role in
  // it, so the router knows where they can go without asking again. Null
  // when the engine didn't answer. Every server function still checks.
  const workspaces = viewer ? await visibleWorkspaces(viewer).catch(() => null) : []
  // The engine URL for connect snippets rides along too, so the database
  // overview doesn't need a round trip of its own. Tokens never leave the
  // server. Passkeys only work on the dashboard URL, not on an extra origin.
  return {
    viewer,
    workspaces,
    // The workspace the browser last had open, for pages outside one.
    lastWorkspace: viewer ? (getCookie(LAST_WORKSPACE_COOKIE) ?? null) : null,
    needsSetup: users === 0,
    engineUrl: viewer ? config().publicEngineUrl : null,
    passkeys: passkeysAvailable(getRequest()),
    dashboardUrl: config().publicUrl,
  }
})

async function signIn(emailValue: string, passwordValue: string) {
  const request = getRequest()
  const auth = await getAuth(request)
  // The cookie plugin writes the session cookie onto this server function's response.
  await auth.api.signInEmail({ body: { email: emailValue, password: passwordValue }, headers: authHeaders(request) })
}

// Only a UserError's message reaches the page. Anything else (SQLite, Better
// Auth) is logged on the server and shown as a generic message.
function fail(e: unknown): never {
  if (e instanceof UserError) throw new Error(e.message)
  console.error(e)
  throw new Error("Something went wrong. Check the dashboard logs.")
}

// The service token proves the person setting up has access to the server,
// so whoever reaches a fresh dashboard first can't claim it.
export const setupOwner = createServerFn({ method: "POST" })
  .validator(z.object({ serviceToken: z.string().trim().min(1).max(300), name, email, password }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    if (!sameSecret(data.serviceToken, engineToken())) throw new Error("That isn't this engine's service token.")
    try {
      await createOwner({ email: data.email, name: data.name, password: data.password })
    } catch (e) {
      fail(e)
    }
    await signIn(data.email.toLowerCase(), data.password)
    return { ok: true }
  })

export const getInvite = createServerFn({ method: "GET" })
  .validator(z.object({ token }))
  .handler(async ({ data }) => {
    const invite = await findInvite(data.token)
    // Only whether it makes an owner: workspace names aren't for someone who hasn't joined yet.
    return invite ? { email: invite.email, owner: invite.owner } : null
  })

export const acceptInvite = createServerFn({ method: "POST" })
  .validator(z.object({ token, name, password }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    let signedUp: { email: string }
    try {
      signedUp = await accept(data.token, { name: data.name, password: data.password })
    } catch (e) {
      fail(e)
    }
    await signIn(signedUp.email, data.password)
    return { ok: true }
  })
