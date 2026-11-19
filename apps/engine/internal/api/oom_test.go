package api_test

import (
	"strings"
	"testing"

	"github.com/zakiego/lumbung/apps/engine/internal/config"
)

// sortsPayloads makes SQLite keep every payload in its sorter (GROUP BY
// copies the columns its aggregates read, and temp storage in memory never
// spills): over 16 MiB against the 8 MiB ceiling below.
const sortsPayloads = "SELECT done, count(*), sum(length(payload)) FROM events GROUP BY 1"

// A statement that runs SQLite out of memory used to panic inside the
// driver and, on the MCP SDK's own goroutine, end the process. Through each
// entry point it now fails with memory_limit, and the next statement on the
// same database works.
func TestOutOfMemoryAtEveryEntryPoint(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.ConnMaxMemory = 8 << 20 })
	e.createDB("big")
	e.ok(e.sql(e.admin, "big", "CREATE TABLE events(id INTEGER PRIMARY KEY, done INTEGER, payload TEXT)"), 200)
	for i := range 16 {
		e.ok(e.sql(e.admin, "big", "INSERT INTO events(done, payload) VALUES (?, replace(hex(zeroblob(524288)), '0', 'x'))", i%2), 200)
	}
	count := func(where string) {
		t.Helper()
		r := e.ok(e.sql(e.admin, "big", "SELECT count(*) FROM events"), 200).json(t)
		if v := r["rows"].([]any)[0].([]any)[0].(map[string]any)["value"]; v != "16" {
			t.Fatalf("after %s: count %v", where, v)
		}
	}

	// REST.
	golden(t, "memory-limit", e.fails(e.sql(e.admin, "big", sortsPayloads), 422, "memory_limit"))
	count("REST")

	// MCP: the query tool reports the error instead of the process exiting.
	s := e.mcp(e.admin)
	_, msg := call(t, s, "query", map[string]any{"database": "big", "sql": sortsPayloads})
	if !strings.Contains(msg, "memory_limit") {
		t.Fatalf("MCP query: %q", msg)
	}
	if out, msg := call(t, s, "query", map[string]any{"database": "big", "sql": "SELECT count(*) AS n FROM events"}); msg != "" || out == nil {
		t.Fatalf("MCP query after: %v %q", out, msg)
	}
	count("MCP")

	// libSQL, on its own and inside an interactive transaction.
	wantErr(t, e.one(e.admin, "big", "main", sortsPayloads), "memory_limit", "libSQL execute")
	h := e.libsql(e.admin, "big", "main")
	h.run("BEGIN")
	wantErr(t, h.pipeline(exec(sortsPayloads))[0], "memory_limit", "libSQL in a transaction")
	h.pipeline(closeReq)
	if got := rows(t, h.ok(e.one(e.admin, "big", "main", "SELECT count(*) FROM events"))["result"].(map[string]any)); len(got) != 1 {
		t.Fatalf("libSQL after: %v", got)
	}
	count("libSQL")
}
