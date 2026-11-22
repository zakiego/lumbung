package engine

import (
	"context"
	"errors"
	"sync"
	"time"
)

// gate serializes work on one branch: requests share it, and operations that
// replace or remove the branch's file (reset, delete, import, deleting the
// database) hold it alone. A waiting exclusive holder blocks new requests, so
// it can't starve.
//
// Unlike sync.RWMutex it honors a context, so a request waiting behind a long
// reset gives up at its statement timeout.
type gate struct {
	mu      sync.Mutex
	readers int
	writer  bool
	waiting int // exclusive holders waiting
	changed chan struct{}

	// seq counts changes to the branch for WaitChange: commits that changed
	// rows or the schema, and every exclusive hold (a reset, import or
	// delete). moved is closed and replaced on each one. Neither touches the
	// gate's own state, so a wait never blocks maintenance.
	seq   uint64
	moved chan struct{}
}

func newGate() *gate { return &gate{changed: make(chan struct{}), moved: make(chan struct{})} }

// bump records a change and wakes every WaitChange on the branch.
func (g *gate) bump() {
	g.mu.Lock()
	g.bumpLocked()
	g.mu.Unlock()
}

func (g *gate) bumpLocked() {
	g.seq++
	close(g.moved)
	g.moved = make(chan struct{})
}

// position returns the change count and the channel the next change closes.
func (g *gate) position() (uint64, chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.seq, g.moved
}

// notify wakes every waiter. Call with mu held.
func (g *gate) notify() {
	close(g.changed)
	g.changed = make(chan struct{})
}

func (g *gate) wait(ctx context.Context, ch chan struct{}) error {
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// share takes the gate for a request.
func (g *gate) share(ctx context.Context) error {
	for {
		g.mu.Lock()
		if !g.writer && g.waiting == 0 {
			g.readers++
			g.mu.Unlock()
			return nil
		}
		ch := g.changed
		g.mu.Unlock()
		if err := g.wait(ctx, ch); err != nil {
			return err
		}
	}
}

func (g *gate) unshare() {
	g.mu.Lock()
	g.readers--
	g.notify()
	g.mu.Unlock()
}

// exclusiveWith takes the gate alone, waiting for requests in flight to
// finish, and calls before ahead of every attempt. Callers use it to roll
// back interactive transactions on the branch, including ones that began
// during a priority break.
//
// While it waits it blocks new requests, but only for priorityWindow at a
// time: if the requests in flight haven't drained by then (a long download
// or copy holds the gate for minutes), it lets new requests through for a
// moment and tries again, so a waiting reset can't take the branch offline.
func (g *gate) exclusiveWith(ctx context.Context, before func()) error {
	for {
		if before != nil {
			before()
		}
		if err := g.tryExclusive(ctx, priorityWindow); err == nil {
			return nil
		} else if ctx.Err() != nil {
			return err
		}
		select {
		case <-time.After(priorityBreak):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

var (
	priorityWindow = 5 * time.Second
	priorityBreak  = 500 * time.Millisecond
)

var errWindow = errors.New("engine: priority window passed")

func (g *gate) tryExclusive(ctx context.Context, window time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	g.mu.Lock()
	g.waiting++
	g.mu.Unlock()
	for {
		g.mu.Lock()
		if !g.writer && g.readers == 0 {
			g.writer = true
			g.waiting--
			g.mu.Unlock()
			return nil
		}
		ch := g.changed
		g.mu.Unlock()
		if err := g.wait(ctx, ch); err != nil {
			g.mu.Lock()
			g.waiting--
			g.notify()
			g.mu.Unlock()
			return errWindow
		}
	}
}

// release ends an exclusive hold. The holder may have replaced or removed
// the branch's file, so it counts as a change.
func (g *gate) release() {
	g.mu.Lock()
	g.writer = false
	g.notify()
	g.bumpLocked()
	g.mu.Unlock()
}

// gates hands out one gate per branch id. Gates are tiny and never freed:
// a long-running instance holds one per branch it has ever served.
type gates struct {
	mu sync.Mutex
	m  map[string]*gate
}

func (gs *gates) get(branchID string) *gate {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if gs.m == nil {
		gs.m = map[string]*gate{}
	}
	g, ok := gs.m[branchID]
	if !ok {
		g = newGate()
		gs.m[branchID] = g
	}
	return g
}

// maintenanceTimeout bounds how long a reset, delete or import waits for
// requests in flight on the branch.
const maintenanceTimeout = 2 * time.Minute
