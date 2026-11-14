import { describe, expect, test } from "vitest"
import { splitSql, stripLeadingComments, textToRun } from "./sql-split"

const sqls = (text: string) => splitSql(text).map((s) => s.sql)

describe("splitSql", () => {
  test("splits at semicolons and drops empty statements", () => {
    expect(sqls("SELECT 1; SELECT 2;;  \n")).toEqual(["SELECT 1", "SELECT 2"])
    expect(sqls("")).toEqual([])
    expect(sqls("  ;  ")).toEqual([])
  })

  test("keeps a last statement with no semicolon", () => {
    expect(sqls("SELECT 1;\nSELECT 2")).toEqual(["SELECT 1", "SELECT 2"])
  })

  test("ignores semicolons in strings, quoted names and comments", () => {
    const text = `INSERT INTO t VALUES ('a;b', 'it''s; fine');
SELECT "x;y", \`z;w\`, [v;u] FROM t; -- a comment; with a semicolon
/* block; comment */ SELECT 3`
    expect(sqls(text)).toEqual([
      "INSERT INTO t VALUES ('a;b', 'it''s; fine')",
      'SELECT "x;y", `z;w`, [v;u] FROM t',
      "-- a comment; with a semicolon\n/* block; comment */ SELECT 3",
    ])
  })

  test("drops a statement that is only comments", () => {
    expect(sqls("SELECT 1; -- done\n/* nothing */")).toEqual(["SELECT 1"])
  })

  test("keeps a trigger body whole", () => {
    const trigger = `CREATE TRIGGER log_insert AFTER INSERT ON t BEGIN
  INSERT INTO log VALUES (new.id, CASE WHEN new.x > 0 THEN 'pos' ELSE 'neg' END);
  UPDATE counts SET n = n + 1;
END`
    expect(sqls(`${trigger};\nSELECT 1;`)).toEqual([trigger, "SELECT 1"])
    expect(sqls(`create temp trigger x before delete on t begin select 1; end; select 2`)).toEqual([
      "create temp trigger x before delete on t begin select 1; end",
      "select 2",
    ])
  })

  test("a trigger after a comment is still a trigger", () => {
    expect(sqls("-- make it\nCREATE TRIGGER x AFTER INSERT ON t BEGIN SELECT 1; END;")).toHaveLength(1)
  })

  test("BEGIN outside a trigger is an ordinary statement", () => {
    expect(sqls("BEGIN; SELECT 1; COMMIT;")).toEqual(["BEGIN", "SELECT 1", "COMMIT"])
  })

  test("an unclosed string runs to the end", () => {
    expect(sqls("SELECT 'oops; SELECT 2")).toEqual(["SELECT 'oops; SELECT 2"])
  })

  test("reports where each statement sits in the text", () => {
    const text = "  SELECT 1 ;\n\nSELECT 2"
    for (const s of splitSql(text)) expect(text.slice(s.start, s.end)).toBe(s.sql)
  })
})

test("stripLeadingComments", () => {
  expect(stripLeadingComments("  -- a\n /* b */ SELECT")).toBe("SELECT")
  expect(stripLeadingComments("-- only")).toBe("")
})

test("textToRun prefers a non-blank selection", () => {
  const text = "SELECT 1; SELECT 2"
  expect(textToRun(text, 10, 18)).toBe("SELECT 2")
  expect(textToRun(text, 4, 4)).toBe(text)
  expect(textToRun(text, 9, 10)).toBe(text)
})
