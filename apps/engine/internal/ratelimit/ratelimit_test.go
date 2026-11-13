package ratelimit

import (
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(2, 3)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("burst %d refused", i)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait <= 0 || wait > time.Second {
		t.Fatalf("over burst: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("keys aren't independent")
	}
	now = now.Add(500 * time.Millisecond)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("refill")
	}
	l2 := New(1, 1)
	l2.now = func() time.Time { return now }
	if ok, _ := l2.Peek("k"); !ok {
		t.Fatal("peek on a fresh key")
	}
	l2.Allow("k")
	if ok, _ := l2.Peek("k"); ok {
		t.Fatal("peek after the burst is spent")
	}
	if ok, _ := New(0, 0).Allow("x"); !ok {
		t.Fatal("zero rate should allow")
	}
}
