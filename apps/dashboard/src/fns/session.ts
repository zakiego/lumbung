// Session, first-run setup and invite acceptance.
import { createServerFn } from "@tanstack/react-start"
import { getRequest } from "@tanstack/react-start/server"
import { z } from "zod"
import { authHeaders, getAuth } from "@/server/auth"
import { engineToken } from "@/server/config"
import { requireSameOrigin, getViewer } from "@/server/session"
import { UserError, acceptInvite as accept, createOwner, findInvite, sameSecret, userCount } from "@/server/users"

const email = z.string().trim().email().max(200)
const name = z.string().trim().min(1).max(100)
const password = z.string().min(1).max(200)
const token = z.string().min(10).max(200)

export const getSession = createServerFn({ method: "GET" }).handler(async () => {
  const [viewer, users] = await Promise.all([getViewer(), userCount()])
  return { viewer, needsSetup: users === 0 }
})

async function signIn(emailValue: string, passwordValue: string) {
  const auth = await getAuth()
  // The cookie plugin writes the session cookie onto this server function's response.
  await auth.api.signInEmail({ body: { email: emailValue, password: passwordValue }, headers: authHeaders(getRequest()) })
}

function fail(e: unknown): never {
  if (e instanceof UserError) throw new Error(e.message)
  throw e
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
    return invite ? { email: invite.email, role: invite.role } : null
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
