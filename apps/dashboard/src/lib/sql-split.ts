// Cuts SQL text into statements, the same way the engine's sqlsplit does:
// at each semicolon outside quotes, bracketed identifiers and comments, and
// keeping a CREATE TRIGGER body (which holds semicolons) whole up to its END.

export interface SqlStatement {
  sql: string
  /** Offsets of `sql` in the text, for mapping a statement back to the editor. */
  start: number
  end: number
}

const TRIGGER_START = /^\s*CREATE\s+(?:TEMP\s+|TEMPORARY\s+)?TRIGGER\b/i

/** Removes leading comments and whitespace, to look at how a statement starts. */
export function stripLeadingComments(s: string): string {
  let t = s
  for (;;) {
    t = t.replace(/^\s+/, "")
    if (t.startsWith("--")) {
      const i = t.indexOf("\n")
      if (i < 0) return ""
      t = t.slice(i + 1)
    } else if (t.startsWith("/*")) {
      const i = t.indexOf("*/", 2)
      if (i < 0) return ""
      t = t.slice(i + 2)
    } else {
      return t
    }
  }
}

/** The bare words of SQL text, skipping strings, quoted identifiers and comments. */
function words(s: string): string[] {
  const out: string[] = []
  for (let i = 0; i < s.length; i++) {
    const c = s[i]!
    if (c === "'" || c === '"' || c === "`" || c === "[") {
      const close = c === "[" ? "]" : c
      i++
      while (i < s.length && s[i] !== close) i++
    } else if (c === "-" && s[i + 1] === "-") {
      while (i < s.length && s[i] !== "\n") i++
    } else if (c === "/" && s[i + 1] === "*") {
      i += 2
      while (i + 1 < s.length && !(s[i] === "*" && s[i + 1] === "/")) i++
      i++
    } else if (/[A-Za-z_]/.test(c)) {
      let j = i
      while (j < s.length && /[A-Za-z0-9_$]/.test(s[j]!)) j++
      out.push(s.slice(i, j))
      i = j - 1
    }
  }
  return out
}

/** Whether a CREATE TRIGGER's body has ended: every BEGIN and CASE is matched by an END. */
function blockClosed(stmt: string): boolean {
  let depth = 0
  let opened = false
  for (const w of words(stmt)) {
    const u = w.toUpperCase()
    if (u === "BEGIN") {
      depth++
      opened = true
    } else if (u === "CASE") depth++
    else if (u === "END") depth--
  }
  return opened && depth <= 0
}

function push(out: SqlStatement[], text: string, start: number, end: number) {
  const chunk = text.slice(start, end)
  if (stripLeadingComments(chunk) === "") return
  const lead = chunk.length - chunk.trimStart().length
  const sql = chunk.trim()
  out.push({ sql, start: start + lead, end: start + lead + sql.length })
}

export function splitSql(text: string): SqlStatement[] {
  const out: SqlStatement[] = []
  let start = 0
  let quote = ""
  let line = false
  let block = false
  for (let i = 0; i < text.length; i++) {
    const c = text[i]!
    if (line) {
      if (c === "\n") line = false
    } else if (block) {
      if (c === "*" && text[i + 1] === "/") {
        block = false
        i++
      }
    } else if (quote) {
      if (c === quote) quote = ""
    } else if (c === "'" || c === '"' || c === "`") {
      quote = c
    } else if (c === "[") {
      quote = "]"
    } else if (c === "-" && text[i + 1] === "-") {
      line = true
      i++
    } else if (c === "/" && text[i + 1] === "*") {
      block = true
      i++
    } else if (c === ";") {
      const stmt = text.slice(start, i)
      if (TRIGGER_START.test(stripLeadingComments(stmt)) && !blockClosed(stmt)) continue
      push(out, text, start, i)
      start = i + 1
    }
  }
  push(out, text, start, text.length)
  return out
}

/** The selected text when there is a selection, else the whole text. */
export function textToRun(text: string, selectionStart: number, selectionEnd: number): string {
  const from = Math.min(selectionStart, selectionEnd)
  const to = Math.max(selectionStart, selectionEnd)
  return to > from && text.slice(from, to).trim() ? text.slice(from, to) : text
}
