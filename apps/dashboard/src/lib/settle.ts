export type Settled<T> = { ok: true; value: T } | { ok: false; error: string }

// Each card loads on its own, so one slow or failing call never blanks the page.
export function settle<T>(p: Promise<T>): Promise<Settled<T>> {
  return p.then(
    (value) => ({ ok: true as const, value }),
    (e: unknown) => ({ ok: false as const, error: e instanceof Error ? e.message : String(e) }),
  )
}
