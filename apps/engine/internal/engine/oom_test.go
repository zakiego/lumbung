package engine

import (
	"context"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
)

// sortsPayloads makes SQLite keep every payload in its sorter: GROUP BY
// copies the columns its aggregates read, and with temp storage in memory
// the sorter never spills. Over 16 MiB of payloads it needs more than the
// 8 MiB ceiling the tests set. The same sum without GROUP BY streams.
const sortsPayloads = "SELECT done, count(*), sum(length(payload)) FROM events GROUP BY 1"

// bigTextEngine returns an engine whose connections may use 8 MiB, with a
// database "big" holding 16 rows of 1 MiB TEXT, and the database's default
// branch id.
func bigTextEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	e := newEngine(t, func(c *config.Config) { c.ConnMaxMemory = 8 << 20 })
	d := mustCreate(t, e, "big")
	mustQuery(t, e, "big", Write, "CREATE TABLE events(id INTEGER PRIMARY KEY, done INTEGER, payload TEXT)")
	for i := range 16 {
		mustQuery(t, e, "big", Write, "INSERT INTO events(done, payload) VALUES (?, replace(hex(zeroblob(524288)), '0', 'x'))", i%2)
	}
	if r := mustQuery(t, e, "big", Read, "SELECT sum(length(payload)) FROM events"); r.Rows[0][0].Int != 16<<20 {
		t.Fatalf("seeded %v bytes", r.Rows[0][0])
	}
	return e, d.DefaultBranchID
}

// pooled reports how many readers the branch's pool holds open, and whether
// it has a writer.
func pooled(e *Engine, branchID string) (readers int, writer bool) {
	e.poolsMu.Lock()
	p := e.pools[branchID]
	e.poolsMu.Unlock()
	if p == nil {
		return 0, false
	}
	p.mu.Lock()
	readers = p.opened
	p.mu.Unlock()
	select {
	case <-p.writerSem:
		writer = p.writer != nil
		p.writerSem <- struct{}{}
	case <-time.After(5 * time.Second):
		panic("writer still held")
	}
	return readers, writer
}

// A statement that runs SQLite out of memory fails with memory_limit
// instead of panicking, the connection it ran on is closed rather than
// pooled, and the next statement on the database works. Every way a
// statement reaches a connection is covered: read and write sessions, the
// capped query MCP uses, and an interactive transaction.
func TestOutOfMemoryFailsCleanly(t *testing.T) {
	e, branchID := bigTextEngine(t)
	ctx := context.Background()
	target := Target{Workspace: ws(e), Database: "big"}

	readersBefore, _ := pooled(e, branchID)
	_, err := query(e, "big", Read, sortsPayloads)
	wantCode(t, err, apierr.MemoryLimit, "read session")
	if got, _ := pooled(e, branchID); got != readersBefore-1 {
		t.Errorf("readers open after out of memory: %d, want %d (the broken one closed)", got, readersBefore-1)
	}
	if r := mustQuery(t, e, "big", Read, "SELECT count(*) FROM events"); r.Rows[0][0].Int != 16 {
		t.Errorf("read after: %v", r.Rows)
	}

	_, err = e.QueryCapped(ctx, target, stmt(sortsPayloads), 100, 64<<10)
	wantCode(t, err, apierr.MemoryLimit, "capped query")

	_, err = query(e, "big", Write, sortsPayloads)
	wantCode(t, err, apierr.MemoryLimit, "write session")
	if _, writer := pooled(e, branchID); writer {
		t.Error("the writer that ran out of memory is still pooled")
	}
	mustQuery(t, e, "big", Write, "INSERT INTO events(done, payload) VALUES (1, 'small')")

	_, err = e.Batch(ctx, target, Write, []Statement{stmt("DELETE FROM events WHERE payload = 'small'"), stmt(sortsPayloads)})
	wantCode(t, err, apierr.MemoryLimit, "batch")
	if r := mustQuery(t, e, "big", Read, "SELECT count(*) FROM events WHERE payload = 'small'"); r.Rows[0][0].Int != 1 {
		t.Error("the failed batch's delete was kept")
	}

	for _, access := range []Access{Read, Write} {
		tx, err := e.BeginTx(ctx, target, access, TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, stmt(sortsPayloads), e.NewBudget())
		wantCode(t, err, apierr.MemoryLimit, "interactive transaction")
		if !tx.Done() {
			t.Error("the transaction stayed open on a broken connection")
			tx.Rollback()
		}
	}

	if r := mustQuery(t, e, "big", Write, "SELECT count(*) FROM events"); r.Rows[0][0].Int != 17 {
		t.Errorf("write connection after: %v", r.Rows)
	}
	if r := mustQuery(t, e, "big", Read, "WITH t AS MATERIALIZED (SELECT done, length(payload) AS n FROM events) SELECT done, sum(n) FROM t GROUP BY 1"); len(r.Rows) != 2 {
		t.Errorf("rewritten aggregate: %v", r.Rows)
	}
}
