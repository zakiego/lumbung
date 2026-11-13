package engine

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/ncruces/go-sqlite3"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/sqlitex"
)

// writeDump writes a SQL dump of the branch on conn to w (PLAN 6.7). Call it
// inside a read transaction so the dump is consistent. The output loads with
// the sqlite3 shell or with Lumbung's SQL import:
//
//	PRAGMA foreign_keys=OFF;
//	BEGIN TRANSACTION;
//	CREATE TABLE ...;  INSERT INTO ... VALUES (...);   one table at a time
//	DELETE FROM sqlite_sequence; INSERT INTO sqlite_sequence ...
//	indexes, views, triggers
//	COMMIT;
//
// Virtual tables are recreated from their CREATE statement and filled from
// their visible columns; their shadow tables are skipped.
func writeDump(conn *sqlitex.Conn, out io.Writer) error {
	sc, err := readSchema(conn)
	if err != nil {
		return err
	}
	for _, t := range sc.Tables {
		if t.Virtual && contentless(t.SQL) {
			return apierr.Newf(apierr.BadRequest, "table %q is a contentless FTS5 table: its index can't be rebuilt from SQL. Download the branch as a .db file instead", t.Name)
		}
	}
	w := bufio.NewWriterSize(out, 64<<10)
	fmt.Fprintln(w, "PRAGMA foreign_keys=OFF;")
	fmt.Fprintln(w, "BEGIN TRANSACTION;")
	for _, t := range sc.Tables {
		fmt.Fprintf(w, "%s;\n", t.SQL)
		cols := []string{}
		for _, c := range t.Columns {
			if c.Hidden == 0 { // generated and hidden columns aren't insertable
				cols = append(cols, sqlite3.QuoteIdentifier(c.Name))
			}
		}
		if len(cols) == 0 {
			continue
		}
		list := strings.Join(cols, ", ")
		table := sqlite3.QuoteIdentifier(t.Name)
		if err := dumpRows(conn, w, "SELECT "+list+" FROM "+table, "INSERT INTO "+table+" ("+list+") VALUES ("); err != nil {
			return fmt.Errorf("engine: dump %s: %w", t.Name, err)
		}
	}
	var hasSeq bool
	if err := eachRow(conn, "SELECT count(*) FROM sqlite_schema WHERE name = 'sqlite_sequence'", func(s *sqlite3.Stmt) {
		hasSeq = s.ColumnInt(0) > 0
	}); err != nil {
		return err
	}
	if hasSeq {
		fmt.Fprintln(w, "DELETE FROM sqlite_sequence;")
		if err := dumpRows(conn, w, "SELECT name, seq FROM sqlite_sequence", "INSERT INTO sqlite_sequence (name, seq) VALUES ("); err != nil {
			return err
		}
	}
	for _, i := range sc.Indexes {
		if i.SQL != "" {
			fmt.Fprintf(w, "%s;\n", i.SQL)
		}
	}
	for _, v := range sc.Views {
		fmt.Fprintf(w, "%s;\n", v.SQL)
	}
	for _, t := range sc.Triggers {
		fmt.Fprintf(w, "%s;\n", t.SQL)
	}
	fmt.Fprintln(w, "COMMIT;")
	return w.Flush()
}

func dumpRows(conn *sqlitex.Conn, w *bufio.Writer, query, prefix string) error {
	stmt, _, err := conn.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()
	n := stmt.ColumnCount()
	for stmt.Step() {
		w.WriteString(prefix)
		for i := 0; i < n; i++ {
			if i > 0 {
				w.WriteString(", ")
			}
			w.WriteString(quoteColumn(stmt, i))
		}
		w.WriteString(");\n")
	}
	return stmt.Err()
}

// contentless reports whether a virtual table is a contentless FTS5 table,
// whose rows can't be read back to rebuild it.
func contentless(createSQL string) bool {
	norm := strings.ToLower(strings.Join(strings.Fields(createSQL), ""))
	return strings.Contains(norm, "usingfts5") && (strings.Contains(norm, "content=''") || strings.Contains(norm, `content=""`))
}

// quoteColumn renders a value as an SQL literal that reads back as the same
// value and type. sqlite3.Quote isn't used: it writes the float 1.0 as 1
// (an integer when read back) and cuts text at a NUL byte.
func quoteColumn(stmt *sqlite3.Stmt, i int) string {
	switch stmt.ColumnType(i) {
	case sqlite3.INTEGER:
		return strconv.FormatInt(stmt.ColumnInt64(i), 10)
	case sqlite3.FLOAT:
		return quoteFloat(stmt.ColumnFloat(i))
	case sqlite3.TEXT:
		return quoteText(stmt.ColumnRawText(i))
	case sqlite3.BLOB:
		return "x'" + hex.EncodeToString(stmt.ColumnRawBlob(i)) + "'"
	}
	return "NULL"
}

func quoteFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "9.0e999"
	case math.IsInf(f, -1):
		return "-9.0e999"
	case math.IsNaN(f):
		return "NULL" // SQLite never stores NaN
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".e") {
		s += ".0" // keep it a REAL literal
	}
	return s
}

func quoteText(b []byte) string {
	if bytes.IndexByte(b, 0) >= 0 {
		// A text literal can't hold NUL; write the bytes and cast back.
		return "CAST(x'" + hex.EncodeToString(b) + "' AS TEXT)"
	}
	return "'" + strings.ReplaceAll(string(b), "'", "''") + "'"
}
