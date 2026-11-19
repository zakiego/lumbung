package sqlitex

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ncruces/go-sqlite3"
)

// bigTextDB makes a branch file with rows large TEXT values, 1 MiB each, and
// returns its path.
func bigTextDB(t *testing.T, rows int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "big.db")
	w, err := Open(context.Background(), Options{Path: path, Create: true, Limits: Limits{MaxValueBytes: 4 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Exec("CREATE TABLE events(id INTEGER PRIMARY KEY, done INTEGER, payload TEXT)"); err != nil {
		t.Fatal(err)
	}
	stmt, _, err := w.Prepare("INSERT INTO events(done, payload) VALUES (?1 % 2, ?2)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	payload := strings.Repeat("x", 1<<20)
	for i := range rows {
		_ = stmt.BindInt(1, i)
		_ = stmt.BindText(2, payload)
		if stmt.Step(); stmt.Err() != nil {
			t.Fatal(stmt.Err())
		}
		_ = stmt.Reset()
	}
	return path
}

// sortsPayloads makes SQLite keep every payload in its sorter: GROUP BY
// copies the columns its aggregates read, and with temp storage in memory
// the sorter never spills.
const sortsPayloads = "SELECT done, count(*), sum(length(payload)) FROM events GROUP BY 1"

func scalar(c *Conn, sql string) (int64, error) {
	stmt, _, err := c.Prepare(sql)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	var n int64
	if stmt.Step() {
		n = stmt.ColumnInt64(0)
	}
	return n, stmt.Err()
}

// The driver panics on SQLITE_NOMEM, and again from Stmt.Close while
// unwinding. Guard returns an error that maps to NOMEM and marks the
// connection broken; a new connection to the same file works.
func TestGuardOutOfMemory(t *testing.T) {
	path := bigTextDB(t, 16)
	limits := Limits{MaxMemoryBytes: 8 << 20}
	c, err := Open(context.Background(), Options{Path: path, ReadOnly: true, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}

	// A streaming aggregate over the same values fits.
	if err := c.Guard(func() error {
		n, err := scalar(c, "SELECT sum(length(payload)) FROM events")
		if err == nil && n != 16<<20 {
			t.Errorf("sum = %d", n)
		}
		return err
	}); err != nil {
		t.Fatalf("streaming sum: %v", err)
	}

	err = c.Guard(func() error {
		_, err := scalar(c, sortsPayloads)
		return err
	})
	var oom *OutOfMemoryError
	if !errors.As(err, &oom) || !errors.Is(err, sqlite3.NOMEM) {
		t.Fatalf("got %v, want an OutOfMemoryError matching NOMEM", err)
	}
	if oom.Limit != 8<<20 || !strings.Contains(err.Error(), "8 MiB") {
		t.Errorf("error %q, limit %d", err, oom.Limit)
	}
	if !c.Broken() {
		t.Error("connection not marked broken")
	}
	if err := c.Reset(); !errors.Is(err, ErrBroken) {
		t.Errorf("Reset = %v, want ErrBroken", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}

	next, err := Open(context.Background(), Options{Path: path, ReadOnly: true, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if n, err := scalar(next, "SELECT count(*) FROM events"); err != nil || n != 16 {
		t.Errorf("next connection: %d, %v", n, err)
	}
}

func TestGuardOtherPanic(t *testing.T) {
	c := openUser(t, filepath.Join(t.TempDir(), "a.db"), false)
	err := c.Guard(func() error { panic("boom") })
	var pe *PanicError
	if !errors.As(err, &pe) || pe.Value != "boom" || len(pe.Stack) == 0 {
		t.Fatalf("got %#v", err)
	}
	if errors.Is(err, sqlite3.NOMEM) {
		t.Error("a plain panic matched NOMEM")
	}
	if !c.Broken() {
		t.Error("connection not marked broken")
	}
	if err := c.Guard(func() error { return nil }); err != nil {
		t.Errorf("Guard without a panic: %v", err)
	}
}

func TestRecover(t *testing.T) {
	run := func() (err error) {
		defer Recover(&err)
		panic(errors.New(oomPanic))
	}
	if err := run(); !errors.Is(err, sqlite3.NOMEM) {
		t.Errorf("got %v", err)
	}
}
