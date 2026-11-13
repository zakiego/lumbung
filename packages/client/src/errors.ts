import type { Problem } from "./generated.ts"

/**
 * An error from the API. `code` is stable: match on it, not on the message.
 * Network failures have code `network_error` and status 0.
 */
export class LumbungError extends Error {
  readonly code: string
  readonly status: number
  readonly detail: string | undefined
  readonly requestId: string | undefined
  readonly problem: Problem | undefined

  constructor(code: string, status: number, message: string, opts: { detail?: string; requestId?: string; problem?: Problem; cause?: unknown } = {}) {
    super(message, opts.cause === undefined ? undefined : { cause: opts.cause })
    this.name = "LumbungError"
    this.code = code
    this.status = status
    this.detail = opts.detail
    this.requestId = opts.requestId
    this.problem = opts.problem
  }
}
