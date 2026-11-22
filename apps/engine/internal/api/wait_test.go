package api_test

import (
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/api"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
)

const (
	pWait  = "/v1/databases/{db}/wait"
	pBWait = "/v1/databases/{db}/branches/{branch}/wait"
)

// waitAt waits on path (a default-branch or branch wait URL) and returns
// the answer's cursor and changed flag.
func (e *env) waitAt(token, template, path, cursor string, timeout int) (string, bool) {
	e.t.Helper()
	q := url.Values{"timeout": {strconv.Itoa(timeout)}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	m := e.ok(e.do(token, "GET", template, path+"?"+q.Encode(), nil), 200).json(e.t)
	return m["cursor"].(string), m["changed"].(bool)
}

func TestWaitREST(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("app")
	e.ok(e.sql(e.admin, "app", "CREATE TABLE t(x)"), 200)

	c, changed := e.waitAt(e.admin, pWait, "/v1/databases/app/wait", "", 30)
	if c == "" || changed {
		t.Fatalf("first wait: %q %v", c, changed)
	}
	if c2, changed := e.waitAt(e.admin, pWait, "/v1/databases/app/wait", c, 0); c2 != c || changed {
		t.Fatalf("zero timeout: %q %v", c2, changed)
	}

	// A held wait wakes on a REST write, and the branch URL names the same
	// state as the default one.
	done := make(chan bool, 1)
	go func() {
		_, ch := e.waitAt(e.admin, pBWait, "/v1/databases/app/branches/main/wait", c, 30)
		done <- ch
	}()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	e.ok(e.sql(e.admin, "app", "INSERT INTO t VALUES (1)"), 200)
	select {
	case ch := <-done:
		if !ch {
			t.Fatal("woke without a change")
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("woke after %v", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a REST write didn't wake the wait")
	}

	// A read token can wait.
	read, _ := e.token(map[string]any{"name": "reader", "access": "read"})
	if c, _ := e.waitAt(read, pWait, "/v1/databases/app/wait", "", 0); c == "" {
		t.Fatal("read token: no cursor")
	}

	// The timeout is capped.
	e.fails(e.do(e.admin, "GET", pWait, "/v1/databases/app/wait?timeout=61", nil), 422, "invalid_request")
	e.fails(e.do(e.admin, "GET", pBWait, "/v1/databases/app/branches/nope/wait", nil), 404, "branch_not_found")
}

func TestWaitWakesOnLibSQLAndMCPWrites(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("app")
	e.ok(e.sql(e.admin, "app", "CREATE TABLE t(x)"), 200)

	writers := map[string]func(){
		"libsql": func() {
			h := e.libsql(e.admin, "app", "main")
			if r := h.pipeline(exec("INSERT INTO t VALUES (1)"), closeReq); r[0]["type"] != "ok" {
				t.Fatalf("libsql: %v", r)
			}
		},
		"mcp": func() {
			s := e.mcp(e.admin)
			if _, msg := call(t, s, "execute", map[string]any{"database": "app", "sql": "INSERT INTO t VALUES (2)"}); msg != "" {
				t.Fatalf("mcp: %s", msg)
			}
		},
	}
	for name, write := range writers {
		c, _ := e.waitAt(e.admin, pWait, "/v1/databases/app/wait", "", 0)
		done := make(chan bool, 1)
		go func() {
			_, ch := e.waitAt(e.admin, pWait, "/v1/databases/app/wait", c, 30)
			done <- ch
		}()
		time.Sleep(100 * time.Millisecond)
		write()
		select {
		case ch := <-done:
			if !ch {
				t.Fatalf("%s: woke without a change", name)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("a %s write didn't wake the wait", name)
		}
	}
}

func TestWaitNotCountedInStats(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("app")
	e.ok(e.sql(e.admin, "app", "CREATE TABLE t(x)"), 200)
	before := e.ok(e.do(e.admin, "GET", pBranch+"/stats", "/v1/databases/app/branches/main/stats", nil), 200).json(t)["queries"]
	c, _ := e.waitAt(e.admin, pWait, "/v1/databases/app/wait", "", 0)
	for range 5 {
		e.waitAt(e.admin, pWait, "/v1/databases/app/wait", c, 0)
	}
	after := e.ok(e.do(e.admin, "GET", pBranch+"/stats", "/v1/databases/app/branches/main/stats", nil), 200).json(t)["queries"]
	if before != after {
		t.Fatalf("queries went from %v to %v on waits alone", before, after)
	}
}

func TestWaitWorkspaceIsolation(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("app")
	e.ok(e.sql(e.admin, "app", "CREATE TABLE t(x)"), 200)
	e.ok(e.do(e.admin, "POST", "/v1/workspaces", "/v1/workspaces", map[string]any{"name": "acme"}), 201)
	e.ok(e.do(e.admin, "POST", "", "/v1/workspaces/acme/databases", map[string]any{"name": "app"}), 201)
	e.ok(e.do(e.admin, "POST", "", "/v1/workspaces/acme/databases/app/query", map[string]any{"sql": "CREATE TABLE t(x)"}), 200)

	c, _ := e.waitAt(e.admin, pWait, "/v1/databases/app/wait", "", 0)
	done := make(chan bool, 1)
	go func() {
		_, ch := e.waitAt(e.admin, pWait, "/v1/databases/app/wait", c, 2)
		done <- ch
	}()
	time.Sleep(100 * time.Millisecond)
	// The same name in another workspace is another database.
	e.ok(e.do(e.admin, "POST", "", "/v1/workspaces/acme/databases/app/query", map[string]any{"sql": "INSERT INTO t VALUES (1)"}), 200)
	if <-done {
		t.Fatal("a write in another workspace woke the wait")
	}
}

func TestWaitLimitsAndDrain(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.RateLimit, c.RateBurst = 1000, 1000 })
	e.createDB("app")
	e.ok(e.sql(e.admin, "app", "CREATE TABLE t(x)"), 200)
	c, _ := e.waitAt(e.admin, pWait, "/v1/databases/app/wait", "", 0)

	// Fill the token's waits; one more is refused.
	var wg sync.WaitGroup
	for range api.MaxWaitsPerToken {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.waitAt(e.admin, pWait, "/v1/databases/app/wait", c, 30)
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if e.srv.API.WaitersHeld() == api.MaxWaitsPerToken {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the waits never all started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.fails(e.do(e.admin, "GET", pWait, "/v1/databases/app/wait?cursor="+url.QueryEscape(c), nil), 429, "rate_limited")

	// Drain answers them all at once, and new waits answer at once.
	start := time.Now()
	e.srv.API.Drain()
	wg.Wait()
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("drain took %v", d)
	}
	start = time.Now()
	if _, ch := e.waitAt(e.admin, pWait, "/v1/databases/app/wait", c, 30); ch {
		t.Fatal("a wait after drain reported a change")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("a wait after drain took %v", d)
	}
	if n := e.srv.API.WaitersHeld(); n != 0 {
		t.Fatalf("%d waits still counted after they ended", n)
	}
}
