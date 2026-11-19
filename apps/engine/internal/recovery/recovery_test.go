package recovery

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestLog(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer Log(log, "worker")
		panic("boom")
	}()
	<-done
	out := buf.String()
	for _, want := range []string{"recovered panic", "in=worker", "panic=boom", "recovery_test.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q lacks %q", out, want)
		}
	}
}
