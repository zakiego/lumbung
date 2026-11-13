package engine

import (
	"sort"
	"strings"

	"github.com/ncruces/go-sqlite3"

	"github.com/zakiego/lumbung/apps/engine/internal/sqlitex"
)

// Schema is a branch's user schema, read from sqlite_schema (PLAN 6.5).
// SQLite's own tables and the shadow tables behind virtual tables are left
// out.
type Schema struct {
	Tables   []Table   `json:"tables"`
	Indexes  []Index   `json:"indexes"`
	Views    []View    `json:"views"`
	Triggers []Trigger `json:"triggers"`
}

// Table is a table or virtual table.
type Table struct {
	Name    string       `json:"name"`
	Virtual bool         `json:"virtual"`
	SQL     string       `json:"sql"`
	Columns []ColumnInfo `json:"columns"`
}

// ColumnInfo is one column, from PRAGMA table_xinfo.
type ColumnInfo struct {
	Name    string  `json:"name"`
	Type    string  `json:"type"`
	NotNull bool    `json:"not_null"`
	Default *string `json:"default"`
	PK      int     `json:"pk"`     // position in the primary key, 0 if not part of it
	Hidden  int     `json:"hidden"` // 0 normal, 1 hidden (virtual tables), 2 or 3 generated
}

// Index is an index, including ones SQLite made for UNIQUE constraints.
type Index struct {
	Name    string   `json:"name"`
	Table   string   `json:"table"`
	Unique  bool     `json:"unique"`
	SQL     string   `json:"sql"` // empty for an index made by a constraint
	Columns []string `json:"columns"`
}

// View is a view.
type View struct {
	Name string `json:"name"`
	SQL  string `json:"sql"`
}

// Trigger is a trigger.
type Trigger struct {
	Name  string `json:"name"`
	Table string `json:"table"`
	SQL   string `json:"sql"`
}

type schemaRow struct{ typ, name, table, sql string }

// readSchema reads the schema on conn. Call it inside a read transaction so
// every query sees the same schema.
func readSchema(conn *sqlitex.Conn) (*Schema, error) {
	shadow := map[string]bool{}
	virtual := map[string]bool{}
	if err := eachRow(conn, "SELECT name, type FROM pragma_table_list WHERE schema = 'main'", func(s *sqlite3.Stmt) {
		switch s.ColumnText(1) {
		case "shadow":
			shadow[s.ColumnText(0)] = true
		case "virtual":
			virtual[s.ColumnText(0)] = true
		}
	}); err != nil {
		return nil, err
	}

	var rows []schemaRow
	if err := eachRow(conn, "SELECT type, name, tbl_name, coalesce(sql, '') FROM sqlite_schema ORDER BY type, name", func(s *sqlite3.Stmt) {
		rows = append(rows, schemaRow{s.ColumnText(0), s.ColumnText(1), s.ColumnText(2), s.ColumnText(3)})
	}); err != nil {
		return nil, err
	}

	sc := &Schema{Tables: []Table{}, Indexes: []Index{}, Views: []View{}, Triggers: []Trigger{}}
	for _, r := range rows {
		if strings.HasPrefix(r.name, "sqlite_") || shadow[r.name] || shadow[r.table] {
			continue
		}
		switch r.typ {
		case "table":
			t := Table{Name: r.name, SQL: r.sql, Virtual: virtual[r.name], Columns: []ColumnInfo{}}
			err := eachRowArgs(conn, "SELECT name, type, \"notnull\", dflt_value, pk, hidden FROM pragma_table_xinfo(?)", []any{r.name}, func(s *sqlite3.Stmt) {
				c := ColumnInfo{Name: s.ColumnText(0), Type: s.ColumnText(1), NotNull: s.ColumnInt(2) != 0, PK: s.ColumnInt(4), Hidden: s.ColumnInt(5)}
				if s.ColumnType(3) != sqlite3.NULL {
					d := s.ColumnText(3)
					c.Default = &d
				}
				t.Columns = append(t.Columns, c)
			})
			if err != nil {
				return nil, err
			}
			sc.Tables = append(sc.Tables, t)
		case "index":
			ix := Index{Name: r.name, Table: r.table, SQL: r.sql, Columns: []string{}}
			if err := eachRowArgs(conn, "SELECT coalesce(name, '<expr>') FROM pragma_index_info(?) ORDER BY seqno", []any{r.name}, func(s *sqlite3.Stmt) {
				ix.Columns = append(ix.Columns, s.ColumnText(0))
			}); err != nil {
				return nil, err
			}
			if err := eachRowArgs(conn, "SELECT \"unique\" FROM pragma_index_list(?) WHERE name = ?", []any{r.table, r.name}, func(s *sqlite3.Stmt) {
				ix.Unique = s.ColumnInt(0) != 0
			}); err != nil {
				return nil, err
			}
			sc.Indexes = append(sc.Indexes, ix)
		case "view":
			sc.Views = append(sc.Views, View{Name: r.name, SQL: r.sql})
		case "trigger":
			sc.Triggers = append(sc.Triggers, Trigger{Name: r.name, Table: r.table, SQL: r.sql})
		}
	}
	return sc, nil
}

func eachRow(conn *sqlitex.Conn, sql string, f func(*sqlite3.Stmt)) error {
	return eachRowArgs(conn, sql, nil, f)
}

func eachRowArgs(conn *sqlitex.Conn, sql string, args []any, f func(*sqlite3.Stmt)) error {
	stmt, _, err := conn.Prepare(sql)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, a := range args {
		if err := stmt.BindText(i+1, a.(string)); err != nil {
			return err
		}
	}
	for stmt.Step() {
		f(stmt)
	}
	return stmt.Err()
}

// DDL lists the statements that recreate the schema, tables first.
func (s *Schema) DDL() []string {
	out := []string{}
	for _, t := range s.Tables {
		out = append(out, t.SQL)
	}
	for _, i := range s.Indexes {
		if i.SQL != "" {
			out = append(out, i.SQL)
		}
	}
	for _, v := range s.Views {
		out = append(out, v.SQL)
	}
	for _, t := range s.Triggers {
		out = append(out, t.SQL)
	}
	return out
}

// Change is one difference between two schemas.
type Change struct {
	Kind    string         `json:"kind"` // table, index, view, trigger
	Name    string         `json:"name"`
	Change  string         `json:"change"` // added, removed, changed
	Before  string         `json:"before"` // DDL on the base side, empty if added
	After   string         `json:"after"`  // DDL on this branch, empty if removed
	Columns *ColumnChanges `json:"columns,omitempty"`
}

// ColumnChanges details a changed table.
type ColumnChanges struct {
	Added   []ColumnInfo   `json:"added"`
	Removed []ColumnInfo   `json:"removed"`
	Changed []ColumnChange `json:"changed"`
}

// ColumnChange is a column present on both sides with different details.
type ColumnChange struct {
	Name   string     `json:"name"`
	Before ColumnInfo `json:"before"`
	After  ColumnInfo `json:"after"`
}

// Diff is how a branch's schema differs from a base branch's.
type Diff struct {
	Branch    string   `json:"branch"`
	Against   string   `json:"against"`
	Changes   []Change `json:"changes"`
	BranchDDL []string `json:"branch_ddl"`
	BaseDDL   []string `json:"against_ddl"`
}

type object struct {
	kind, name, sql string
	cols            []ColumnInfo
}

func objects(s *Schema) map[string]object {
	m := map[string]object{}
	for _, t := range s.Tables {
		m["table:"+t.Name] = object{"table", t.Name, t.SQL, t.Columns}
	}
	for _, i := range s.Indexes {
		m["index:"+i.Name] = object{"index", i.Name, i.SQL + "|" + i.Table + "|" + strings.Join(i.Columns, ","), nil}
	}
	for _, v := range s.Views {
		m["view:"+v.Name] = object{"view", v.Name, v.SQL, nil}
	}
	for _, t := range s.Triggers {
		m["trigger:"+t.Name] = object{"trigger", t.Name, t.SQL, nil}
	}
	return m
}

func ddlOf(o object) string {
	if o.kind == "index" {
		return strings.SplitN(o.sql, "|", 2)[0]
	}
	return o.sql
}

// diffSchemas reports what changed from base to branch.
func diffSchemas(base, branch *Schema) []Change {
	a, b := objects(base), objects(branch)
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	kindOrder := map[string]int{"table": 0, "index": 1, "view": 2, "trigger": 3}
	sort.Slice(sorted, func(i, j int) bool {
		ki, kj := strings.SplitN(sorted[i], ":", 2), strings.SplitN(sorted[j], ":", 2)
		if kindOrder[ki[0]] != kindOrder[kj[0]] {
			return kindOrder[ki[0]] < kindOrder[kj[0]]
		}
		return ki[1] < kj[1]
	})
	changes := []Change{}
	for _, k := range sorted {
		before, inA := a[k]
		after, inB := b[k]
		switch {
		case inA && !inB:
			changes = append(changes, Change{Kind: before.kind, Name: before.name, Change: "removed", Before: ddlOf(before)})
		case !inA && inB:
			changes = append(changes, Change{Kind: after.kind, Name: after.name, Change: "added", After: ddlOf(after)})
		default:
			cols := diffColumns(before.cols, after.cols)
			if before.sql == after.sql && cols == nil {
				continue
			}
			c := Change{Kind: after.kind, Name: after.name, Change: "changed", Before: ddlOf(before), After: ddlOf(after)}
			if after.kind == "table" {
				if cols == nil {
					cols = &ColumnChanges{Added: []ColumnInfo{}, Removed: []ColumnInfo{}, Changed: []ColumnChange{}}
				}
				c.Columns = cols
			}
			changes = append(changes, c)
		}
	}
	return changes
}

func sameColumn(x, y ColumnInfo) bool {
	if x.Name != y.Name || x.Type != y.Type || x.NotNull != y.NotNull || x.PK != y.PK || x.Hidden != y.Hidden {
		return false
	}
	if (x.Default == nil) != (y.Default == nil) {
		return false
	}
	return x.Default == nil || *x.Default == *y.Default
}

// diffColumns returns nil when the columns are the same.
func diffColumns(before, after []ColumnInfo) *ColumnChanges {
	if before == nil && after == nil {
		return nil
	}
	cc := &ColumnChanges{Added: []ColumnInfo{}, Removed: []ColumnInfo{}, Changed: []ColumnChange{}}
	old := map[string]ColumnInfo{}
	for _, c := range before {
		old[c.Name] = c
	}
	seen := map[string]bool{}
	for _, c := range after {
		seen[c.Name] = true
		o, ok := old[c.Name]
		switch {
		case !ok:
			cc.Added = append(cc.Added, c)
		case !sameColumn(o, c):
			cc.Changed = append(cc.Changed, ColumnChange{Name: c.Name, Before: o, After: c})
		}
	}
	for _, c := range before {
		if !seen[c.Name] {
			cc.Removed = append(cc.Removed, c)
		}
	}
	if len(cc.Added)+len(cc.Removed)+len(cc.Changed) == 0 {
		return nil
	}
	return cc
}
