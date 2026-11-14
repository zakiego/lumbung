type Warning = { component?: string; message?: string }

/**
 * The last warning ui-sdk recorded, to compare before and after a render. The
 * test setup makes most warnings throw, but one raised from a timer throws
 * outside the test, so a test that cares asks here too.
 */
export function lastUiSdkWarning(): Warning | undefined {
  const history = (globalThis as unknown as Record<symbol, { warnings?: Warning[] } | undefined>)[Symbol.for("ui-sdk.devtools.history@1")]
  return history?.warnings?.at(-1)
}

/** Long enough for ui-sdk's deferred checks, like a tooltip trigger's name, to run. */
export const settle = () => new Promise((r) => setTimeout(r, 5))
