package engine

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// A panic in the janitor's periodic work is logged and the process keeps
// going: the janitor runs on its own goroutine, which nothing else recovers.
func TestJanitorSurvivesPanic(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "a")
	var logs lockedBuffer
	e.log = slog.New(slog.NewTextHandler(&logs, nil))

	// With no meta store, the tick panics on a nil pointer.
	store := e.meta
	e.meta = nil
	e.safeTick(time.Now())
	e.meta = store

	if out := logs.String(); !strings.Contains(out, "recovered panic") || !strings.Contains(out, "in=janitor") {
		t.Fatalf("log: %q", out)
	}
	e.safeTick(time.Now())
	mustQuery(t, e, "a", Read, "SELECT 1")
}
