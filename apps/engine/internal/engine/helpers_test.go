package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
	"github.com/zakiego/lumbung/apps/engine/internal/value"
)

func newEngine(t testing.TB, mod func(*config.Config)) *Engine {
	t.Helper()
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	if mod != nil {
		mod(&cfg)
	}
	e, err := Open(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.freeBytes = func(string) (int64, error) { return 1 << 50, nil }
	t.Cleanup(func() {
		if err := e.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return e
}

// ws is the id of the default workspace, where these tests keep their
// databases and buckets unless a test makes another one.
func ws(e *Engine) string {
	w, err := e.meta.DefaultWorkspace(context.Background())
	if err != nil {
		panic(err)
	}
	return w.ID
}

func mustCreate(t testing.TB, e *Engine, name string) meta.Database {
	t.Helper()
	d, err := e.CreateDatabase(context.Background(), ws(e), name)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func stmt(sql string, args ...any) Statement {
	s := Statement{SQL: sql}
	for _, a := range args {
		switch v := a.(type) {
		case nil:
			s.Args = append(s.Args, value.NullValue())
		case int:
			s.Args = append(s.Args, value.IntValue(int64(v)))
		case int64:
			s.Args = append(s.Args, value.IntValue(v))
		case float64:
			s.Args = append(s.Args, value.FloatValue(v))
		case string:
			s.Args = append(s.Args, value.TextValue(v))
		case []byte:
			s.Args = append(s.Args, value.BlobValue(v))
		default:
			panic("stmt: unsupported arg")
		}
	}
	return s
}

func query(e *Engine, db string, access Access, sql string, args ...any) (*Result, error) {
	return e.Query(context.Background(), Target{Workspace: ws(e), Database: db}, access, stmt(sql, args...))
}

func mustQuery(t testing.TB, e *Engine, db string, access Access, sql string, args ...any) *Result {
	t.Helper()
	r, err := query(e, db, access, sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return r
}

func wantCode(t testing.TB, err error, code string, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: succeeded, want %s", what, code)
	}
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("%s: got non-API error %v, want %s", what, err, code)
	}
	if ae.Code != code {
		t.Fatalf("%s: got %s (%v), want %s", what, ae.Code, ae, code)
	}
}

func scalar(t testing.TB, r *Result) value.Value {
	t.Helper()
	if len(r.Rows) != 1 || len(r.Rows[0]) != 1 {
		t.Fatalf("want one value, got %d rows", len(r.Rows))
	}
	return r.Rows[0][0]
}
