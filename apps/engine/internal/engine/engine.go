// Package engine runs user SQL against branch files and manages databases.
// It is the layer the REST API, MCP and CLI all go through, so the isolation
// rules in PLAN 6.2 are tested here, on the real query path.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
	"github.com/zakiego/lumbung/apps/engine/internal/sqlitex"
	"github.com/zakiego/lumbung/apps/engine/internal/storage"
)

// Engine owns the data directory.
type Engine struct {
	cfg    config.Config
	layout storage.Layout
	meta   *meta.Store
	log    *slog.Logger

	// refused is set when the engine can't serve, e.g. meta.db is newer than
	// this binary. Every operation returns it and /health fails.
	refused error

	poolsMu sync.Mutex
	pools   map[string]*branchPool // by branch id

	usageMu  sync.Mutex
	sizes    map[string]int64 // bytes on disk by branch id
	total    int64
	reserved int64 // room promised to write transactions in flight

	stop chan struct{}
	done chan struct{}

	// freeBytes reports free disk space; replaced in tests.
	freeBytes func(path string) (int64, error)
}

// Open opens the data directory, creating it on first start.
func Open(ctx context.Context, cfg config.Config, log *slog.Logger) (*Engine, error) {
	if log == nil {
		log = slog.Default()
	}
	layout, err := storage.New(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	if err := layout.Ensure(); err != nil {
		return nil, err
	}
	e := &Engine{
		cfg:       cfg,
		layout:    layout,
		log:       log,
		pools:     map[string]*branchPool{},
		sizes:     map[string]int64{},
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		freeBytes: storage.FreeBytes,
	}
	e.meta, err = meta.Open(ctx, layout.Meta())
	if errors.Is(err, meta.ErrNewerSchema) {
		e.refused = apierr.New(apierr.Unavailable, "meta.db is newer than this binary").WithDetail(err.Error())
		log.Error("refusing to serve", "err", err)
	} else if err != nil {
		return nil, err
	} else if err := e.scanUsage(ctx); err != nil {
		_ = e.meta.Close()
		return nil, err
	}
	go e.janitor()
	return e, nil
}

// Close closes every connection and meta.db.
func (e *Engine) Close() error {
	close(e.stop)
	<-e.done
	e.poolsMu.Lock()
	pools := e.pools
	e.pools = map[string]*branchPool{}
	e.poolsMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var errs []error
	for _, p := range pools {
		errs = append(errs, p.close(ctx))
	}
	if e.meta != nil {
		errs = append(errs, e.meta.Close())
	}
	return errors.Join(errs...)
}

// Ready reports whether the engine can serve (the /health check).
func (e *Engine) Ready(ctx context.Context) error {
	if e.refused != nil {
		return e.refused
	}
	if _, err := e.meta.CountDatabases(ctx); err != nil {
		return apierr.New(apierr.Unavailable, "meta.db unreadable").WithDetail(err.Error())
	}
	return nil
}

// Layout returns the data directory layout.
func (e *Engine) Layout() storage.Layout { return e.layout }

func (e *Engine) limits() sqlitex.Limits {
	return sqlitex.Limits{
		MaxValueBytes:  int(e.cfg.MaxValueBytes),
		MaxSQLBytes:    int(e.cfg.MaxSQLBytes),
		MaxParams:      e.cfg.MaxParams,
		MaxMemoryBytes: e.cfg.ConnMaxMemory,
		BusyTimeout:    e.cfg.BusyTimeout,
	}
}

// acquirePool returns the connection pool for a branch and holds a
// reference to it, so eviction and idle close leave it alone until
// releasePool. It evicts the least recently used unheld pool when the
// open-branch cap is reached.
func (e *Engine) acquirePool(branchID, path string) *branchPool {
	e.poolsMu.Lock()
	defer e.poolsMu.Unlock()
	p, ok := e.pools[branchID]
	if !ok {
		if len(e.pools) >= e.cfg.MaxOpenBranches {
			e.evictLocked(1)
		}
		p = newBranchPool(path, e.limits(), e.cfg.ReadersPerBranch)
		e.pools[branchID] = p
	}
	p.refs++
	return p
}

// releasePool drops a reference taken by acquirePool.
func (e *Engine) releasePool(p *branchPool) {
	e.poolsMu.Lock()
	p.refs--
	e.poolsMu.Unlock()
}

// evictable reports whether nothing holds or uses a pool. Call with poolsMu.
func evictable(p *branchPool) (bool, time.Time) {
	if p.refs > 0 {
		return false, time.Time{}
	}
	return p.idleSince()
}

// evictLocked closes up to n idle pools, oldest first. A pool a request
// holds is never evicted, so the cap can be passed briefly.
func (e *Engine) evictLocked(n int) {
	type cand struct {
		id   string
		last time.Time
	}
	var cands []cand
	for id, p := range e.pools {
		if ok, last := evictable(p); ok {
			cands = append(cands, cand{id, last})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].last.Before(cands[j].last) })
	for i := 0; i < n && i < len(cands); i++ {
		p := e.pools[cands[i].id]
		delete(e.pools, cands[i].id)
		go func() { _ = p.close(context.Background()) }()
	}
}

func (e *Engine) janitor() {
	defer close(e.done)
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-e.stop:
			return
		case <-tick.C:
			e.closeIdle(time.Now().Add(-e.cfg.IdleConnTimeout))
		}
	}
}

// closeIdle closes pools unused since before cutoff.
func (e *Engine) closeIdle(cutoff time.Time) {
	e.poolsMu.Lock()
	defer e.poolsMu.Unlock()
	for id, p := range e.pools {
		if ok, last := evictable(p); ok && last.Before(cutoff) {
			delete(e.pools, id)
			go func() { _ = p.close(context.Background()) }()
		}
	}
}

// scanUsage measures every live branch file at start.
func (e *Engine) scanUsage(ctx context.Context) error {
	branches, err := e.meta.ListLiveBranches(ctx)
	if err != nil {
		return err
	}
	for _, b := range branches {
		e.recordSize(b.ID, fileBytes(e.layout.BranchFile(b.DatabaseID, b.ID)))
	}
	return nil
}

// fileBytes is the space a branch takes: its file plus WAL and journal.
func fileBytes(path string) int64 {
	var n int64
	for _, p := range storage.BranchFiles(path) {
		if fi, err := os.Stat(p); err == nil {
			n += fi.Size()
		}
	}
	return n
}

func (e *Engine) recordSize(branchID string, n int64) {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	e.total += n - e.sizes[branchID]
	e.sizes[branchID] = n
}

// TotalBytes is the space all live branches take.
func (e *Engine) TotalBytes() int64 {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	return e.total
}

// checkDisk refuses to create files when the host disk is nearly full.
func (e *Engine) checkDisk() error {
	free, err := e.freeBytes(e.layout.Root)
	if err != nil {
		return fmt.Errorf("engine: free disk: %w", err)
	}
	if free < e.cfg.MinFreeDiskBytes {
		return apierr.Newf(apierr.DiskFull, "only %d bytes free on the data disk, need %d", free, e.cfg.MinFreeDiskBytes)
	}
	return nil
}

// CreateDatabase creates a database with an empty default branch, main.
func (e *Engine) CreateDatabase(ctx context.Context, name string) (meta.Database, error) {
	if e.refused != nil {
		return meta.Database{}, e.refused
	}
	if !ids.ValidName(name) {
		return meta.Database{}, apierr.Newf(apierr.InvalidName, "invalid database name %q", name)
	}
	if _, err := e.meta.GetDatabase(ctx, name); err == nil {
		return meta.Database{}, apierr.Newf(apierr.NameTaken, "a database named %q already exists", name)
	}
	n, err := e.meta.CountDatabases(ctx)
	if err != nil {
		return meta.Database{}, err
	}
	if n >= e.cfg.MaxDatabases {
		return meta.Database{}, apierr.Newf(apierr.LimitReached, "the instance has the most databases allowed (%d)", e.cfg.MaxDatabases)
	}
	if err := e.checkDisk(); err != nil {
		return meta.Database{}, err
	}

	dbID, branchID := ids.New(ids.Database), ids.New(ids.Branch)
	path := e.layout.BranchFile(dbID, branchID)
	if err := os.MkdirAll(e.layout.DatabaseDir(dbID), 0o700); err != nil {
		return meta.Database{}, err
	}
	// Create the file through a writer (which sets WAL mode) before the
	// record exists, so a recorded branch always has its file.
	p := e.acquirePool(branchID, path)
	w, err := p.acquireWriter(ctx, true)
	if err == nil {
		p.releaseWriter(w)
	}
	e.releasePool(p)
	if err != nil {
		e.dropPool(branchID)
		_ = os.RemoveAll(e.layout.DatabaseDir(dbID))
		return meta.Database{}, mapAcquire(err)
	}
	d, _, err := e.meta.CreateDatabase(ctx, name, dbID, branchID, e.cfg.MaxDatabases)
	if err != nil {
		e.dropPool(branchID)
		_ = os.RemoveAll(e.layout.DatabaseDir(dbID))
		return meta.Database{}, err
	}
	e.recordSize(branchID, fileBytes(path))
	return d, nil
}

// dropPool closes and forgets a branch's pool.
func (e *Engine) dropPool(branchID string) {
	e.poolsMu.Lock()
	p, ok := e.pools[branchID]
	delete(e.pools, branchID)
	e.poolsMu.Unlock()
	if ok {
		_ = p.close(context.Background())
	}
}

// target is a resolved database and branch.
type target struct {
	db     meta.Database
	branch meta.Branch
	path   string
}

// resolve finds a database and branch by name or id. An empty branch means
// the database's default branch.
func (e *Engine) resolve(ctx context.Context, dbRef, branchRef string) (target, error) {
	if e.refused != nil {
		return target{}, e.refused
	}
	d, err := e.meta.GetDatabase(ctx, dbRef)
	if err != nil {
		return target{}, err
	}
	if branchRef == "" {
		branchRef = d.DefaultBranchID
	}
	b, err := e.meta.GetBranch(ctx, d.ID, branchRef)
	if err != nil {
		return target{}, err
	}
	return target{db: d, branch: b, path: e.layout.BranchFile(d.ID, b.ID)}, nil
}
