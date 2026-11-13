package engine

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/sqlitex"
)

// branchPool holds the connections of one branch file: one writer and up to
// maxReaders read-only connections (PLAN 6.2: one writer per branch file
// with a small pool of readers).
type branchPool struct {
	path       string
	limits     sqlitex.Limits
	maxReaders int

	writerSem chan struct{} // holds one token; taking it is holding the writer
	writer    *sqlitex.Conn // opened lazily under writerSem

	mu      sync.Mutex
	idle    []*sqlitex.Conn // idle readers
	opened  int             // readers open, idle or in use
	waiters chan struct{}   // signalled when a reader is returned
	inUse   int             // connections handed out
	refs    int             // requests holding the pool; guarded by Engine.poolsMu
	closed  bool
	last    time.Time
}

var errPoolClosed = errors.New("engine: branch pool closed")

func newBranchPool(path string, limits sqlitex.Limits, maxReaders int) *branchPool {
	p := &branchPool{
		path:       path,
		limits:     limits,
		maxReaders: maxReaders,
		writerSem:  make(chan struct{}, 1),
		waiters:    make(chan struct{}, 1),
		last:       time.Now(),
	}
	p.writerSem <- struct{}{}
	return p
}

// acquireWriter waits for the writer, opening it on first use. create makes
// the branch file if it doesn't exist (only when a database or branch is
// being created).
func (p *branchPool) acquireWriter(ctx context.Context, create bool) (*sqlitex.Conn, error) {
	select {
	case <-p.writerSem:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		p.writerSem <- struct{}{}
		return nil, errPoolClosed
	}
	p.inUse++
	p.last = time.Now()
	p.mu.Unlock()
	if p.writer == nil {
		c, err := sqlitex.Open(ctx, sqlitex.Options{Path: p.path, Create: create, Limits: p.limits})
		if err != nil {
			p.releaseWriter(nil)
			return nil, err
		}
		p.writer = c
	}
	return p.writer, nil
}

// releaseWriter resets and returns the writer. A writer that can't be reset
// is closed and reopened on next use.
func (p *branchPool) releaseWriter(c *sqlitex.Conn) {
	if c != nil {
		if err := c.Reset(); err != nil {
			_ = c.Close()
			p.writer = nil
		}
	}
	p.mu.Lock()
	p.inUse--
	p.last = time.Now()
	p.mu.Unlock()
	p.writerSem <- struct{}{}
}

// acquireReader returns an idle reader, opens a new one under the cap, or
// waits for one. The writer is opened first if needed, so the WAL files
// exist before a read-only connection looks for them.
func (p *branchPool) acquireReader(ctx context.Context) (*sqlitex.Conn, error) {
	if err := p.ensureWriterOpen(ctx); err != nil {
		return nil, err
	}
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			p.signal() // pass on a wake-up close may be waiting for
			return nil, errPoolClosed
		}
		if n := len(p.idle); n > 0 {
			c := p.idle[n-1]
			p.idle = p.idle[:n-1]
			p.inUse++
			p.last = time.Now()
			p.mu.Unlock()
			return c, nil
		}
		if p.opened < p.maxReaders {
			p.opened++
			p.inUse++
			p.last = time.Now()
			p.mu.Unlock()
			c, err := sqlitex.Open(ctx, sqlitex.Options{Path: p.path, ReadOnly: true, Limits: p.limits})
			if err != nil {
				p.mu.Lock()
				p.opened--
				p.inUse--
				p.mu.Unlock()
				p.signal()
				return nil, err
			}
			return c, nil
		}
		p.mu.Unlock()
		select {
		case <-p.waiters:
		case <-time.After(20 * time.Millisecond): // the one-slot signal may have gone elsewhere
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (p *branchPool) signal() {
	select {
	case p.waiters <- struct{}{}:
	default:
	}
}

func (p *branchPool) releaseReader(c *sqlitex.Conn) {
	keep := c.Reset() == nil
	p.mu.Lock()
	p.inUse--
	p.last = time.Now()
	if keep && !p.closed {
		p.idle = append(p.idle, c)
		c = nil
	} else {
		p.opened--
	}
	p.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
	p.signal()
}

func (p *branchPool) ensureWriterOpen(ctx context.Context) error {
	if p.hasWriter() {
		return nil
	}
	c, err := p.acquireWriter(ctx, false)
	if err != nil {
		return err
	}
	p.releaseWriter(c)
	return nil
}

func (p *branchPool) hasWriter() bool {
	select {
	case <-p.writerSem:
		ok := p.writer != nil
		p.writerSem <- struct{}{}
		return ok
	default:
		// Someone holds the writer, so it's open or being opened by them.
		return true
	}
}

// idleSince reports whether nothing is in use and since when.
func (p *branchPool) idleSince() (bool, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inUse == 0, p.last
}

// close closes every connection once none is in use. It waits for the
// writer, then refuses new work.
func (p *branchPool) close(ctx context.Context) error {
	select {
	case <-p.writerSem:
	case <-ctx.Done():
		return ctx.Err()
	}
	p.mu.Lock()
	p.closed = true
	idle := p.idle
	p.idle = nil
	p.opened -= len(idle)
	p.mu.Unlock()
	for _, c := range idle {
		_ = c.Close()
	}
	// Wait for readers still out; releaseReader closes them because closed is set.
	for {
		p.mu.Lock()
		out := p.opened
		p.mu.Unlock()
		if out == 0 {
			break
		}
		select {
		case <-p.waiters:
		case <-time.After(20 * time.Millisecond): // a wake-up may go to a reader instead
		case <-ctx.Done():
			// Give up on the readers still out (releaseReader closes them
			// when they come back) but don't leak the writer.
			if p.writer != nil {
				_ = p.writer.Close()
				p.writer = nil
			}
			p.writerSem <- struct{}{}
			return ctx.Err()
		}
	}
	var err error
	if p.writer != nil {
		err = p.writer.Close()
		p.writer = nil
	}
	p.writerSem <- struct{}{}
	return err
}
