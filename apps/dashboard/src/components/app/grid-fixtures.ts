// Demo data and the worst case for the data grid and the SQL results
// (break-ui). Shaped like what browseTable and runSql send to the browser.
import type { Cell } from "@/lib/cells"
import type { GridColumnView, GridRowView } from "./table-grid"

const text = (v: string): Cell => ({ t: "text", v, len: [...v].length, cut: false })
const int = (v: string | number): Cell => ({ t: "int", v: String(v) })
const real = (v: string): Cell => ({ t: "real", v })
const NULL: Cell = { t: "null" }

export const demoColumns: GridColumnView[] = [
  { name: "id", type: "INTEGER" },
  { name: "email", type: "TEXT" },
  { name: "name", type: "TEXT" },
  { name: "score", type: "REAL" },
]

export const demoRows: GridRowView[] = [
  { id: "1", cells: [int(1), text("jane@acme.com"), text("Jane Doe"), real("4.5")] },
  { id: "2", cells: [int(2), text("sam@acme.com"), text("Sam Lee"), real("3")] },
  { id: "3", cells: [int(3), text("kai@acme.com"), NULL, real("5")] },
]

export const worstTableName = "customer_lifetime_value_by_acquisition_channel_and_region_2026q3"

/** A 5 MB text value as the engine sends it in a list: the first 1,000 characters and the full length. */
export const bigText: Cell = { t: "text", v: "Lorem ipsum dolor sit amet, ".repeat(36).slice(0, 1000), len: 5_242_880, cut: true }

export const worstColumns: GridColumnView[] = [
  { name: "id", type: "INTEGER" },
  { name: "primary_contact_email_address_for_billing_and_invoices", type: "VARCHAR(320)" },
  { name: "notes", type: "TEXT" },
  { name: "avatar", type: "BLOB" },
  { name: "balance", type: "NUMERIC(20, 4) CHECK (balance >= 0)" },
  { name: "x", type: "" },
  ...Array.from({ length: 294 }, (_, i) => ({ name: `attribute_${String(i + 1).padStart(3, "0")}`, type: "TEXT" })),
]

function worstRow(i: number): GridRowView {
  const rest = Array.from({ length: 294 }, (_, j) => ((i + j) % 7 === 0 ? NULL : text(`v${j}`)))
  const variants: Cell[][] = [
    [int("9223372036854775807"), text("bartholomew.fitzgerald@northwind-industries-holdings.example.com"), bigText, { t: "blob", size: 2_457_600, hex: "89504e470d0a1a0a0000000d49484452" }, real("-1.7976931348623157e+308"), NULL],
    [int("-9223372036854775808"), NULL, text("مرحبا بالعالم, Ünïcödé, 中文, 😀👩‍👩‍👧"), { t: "blob", size: 0, hex: "" }, real("Infinity"), text("")],
    [int(3), text("j@x.co"), text("line one\nline two\ttabbed"), NULL, int("0"), real("0.1")],
  ]
  return { id: String(i + 1), cells: [...variants[i % 3]!, ...rest] }
}

export const worstRows: GridRowView[] = Array.from({ length: 50 }, (_, i) => worstRow(i))
