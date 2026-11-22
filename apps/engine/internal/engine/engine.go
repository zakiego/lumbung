// Package engine runs user SQL against branch files and manages databases.
// It is the layer the REST API, MCP and CLI all go through, so the isolation
// rules in PLAN 6.2 are tested here, on the real query path.
package engine

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
	"github.com/zakiego/lumbung/apps/engine/internal/recovery"
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
	pools   map[string]*branchPool   // by branch id
	closing map[string]chan struct{} // pools being closed, closed when done

	gates gates // per-branch maintenance gates
	// boot tells this process's change cursors from an earlier one's, whose
	// counts started over (WaitChange).
	boot string

	txMu     sync.Mutex
	txs      map[*Tx]struct{} // open interactive transactions
	txClosed bool             // set when closing: no new interactive transactions

	trashMu sync.Mutex // serializes deleting, restoring and purging databases

	usageMu  sync.Mutex
	sizes    map[string]int64 // bytes on disk by branch id
	total    int64
	reserved int64 // room promised to write transactions in flight
	// The same, by workspace, for each workspace's own cap on database
	// bytes (PLAN 17.5). owner is the workspace of each branch in sizes.
	owner      map[string]string
	wsTotal    map[string]int64
	wsReserved map[string]int64

	stop chan struct{}
	done chan struct{}

	closeOnce sync.Once
	closeErr  error
	unlock    func() error // releases the data directory lock

	// freeBytes reports free disk space; replaced in tests.
	freeBytes func(path string) (int64, error)

	// Object storage (objects.go).
	objMu            sync.Mutex
	objReserved      map[string]int64 // bytes promised to writes in flight, by bucket
	objReservedWS    map[string]int64 // the same, by workspace
	objReservedTotal int64
	objCommit        sync.RWMutex // writers share it; the orphan sweep takes it alone
	sealer           *auth.Sealer
	lastSweep        time.Time // janitor goroutine only
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
	unlock, err := layout.Lock()
	if err != nil {
		return nil, err
	}
	opened := false
	defer func() {
		if !opened {
			_ = unlock()
		}
	}()
	e := &Engine{
		unlock:     unlock,
		boot:       rand.Text()[:10],
		cfg:        cfg,
		layout:     layout,
		log:        log,
		pools:      map[string]*branchPool{},
		closing:    map[string]chan struct{}{},
		sizes:      map[string]int64{},
		owner:      map[string]string{},
		wsTotal:    map[string]int64{},
		wsReserved: map[string]int64{},
		txs:        map[*Tx]struct{}{},
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		freeBytes:  storage.FreeBytes,

		objReserved:   map[string]int64{},
		objReservedWS: map[string]int64{},
		lastSweep:     time.Now(),
	}
	key, err := auth.LoadOrCreateKey(layout.AccessKeyFile())
	if err != nil {
		return nil, err
	}
	if e.sealer, err = auth.NewSealer(key); err != nil {
		return nil, err
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
	} else if err := e.removeOrphans(ctx); err != nil {
		_ = e.meta.Close()
		return nil, err
	} else if err := e.sweepObjects(ctx, time.Now(), cfg.ObjectGCDelay); err != nil {
		_ = e.meta.Close()
		return nil, err
	}
	e.removeStaleTemp()
	go e.janitor()
	opened = true
	return e, nil
}

// Close closes every connection and meta.db. Later calls return the first
// call's result.
func (e *Engine) Close() error {
	e.closeOnce.Do(func() { e.closeErr = e.close() })
	return e.closeErr
}

func (e *Engine) close() error {
	close(e.stop)
	<-e.done
	// Interactive transactions hold connections; give them back first.
	e.abortTxs("")
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
	errs = append(errs, e.unlock())
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

func (e *Engine) limits() sqlitex.Limits { return Limits(e.cfg) }

// Limits are the limits user connections run with, for tools outside the
// engine (backups) that open branch files.
func Limits(cfg config.Config) sqlitex.Limits {
	return sqlitex.Limits{
		MaxValueBytes:  int(cfg.MaxValueBytes),
		MaxSQLBytes:    int(cfg.MaxSQLBytes),
		MaxParams:      cfg.MaxParams,
		MaxMemoryBytes: cfg.ConnMaxMemory,
		BusyTimeout:    cfg.BusyTimeout,
	}
}

// acquirePool returns the connection pool for a branch and holds a
// reference to it, so eviction and idle close leave it alone until
// releasePool. It evicts the least recently used unheld pool when the
// open-branch cap is reached.
func (e *Engine) acquirePool(branchID, path string) *branchPool {
	e.poolsMu.Lock()
	defer e.poolsMu.Unlock()
	// A pool still closing holds the file: wait so the new pool never shares
	// a WAL with connections that are about to delete it.
	for {
		ch, closing := e.closing[branchID]
		if !closing {
			break
		}
		e.poolsMu.Unlock()
		<-ch
		e.poolsMu.Lock()
	}
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
		e.closeLocked(cands[i].id)
	}
}

// closeLocked closes a pool in the background, recording it in closing until
// it is done. Call with poolsMu held.
func (e *Engine) closeLocked(branchID string) {
	p := e.pools[branchID]
	delete(e.pools, branchID)
	done := make(chan struct{})
	e.closing[branchID] = done
	go func() {
		defer func() {
			e.poolsMu.Lock()
			delete(e.closing, branchID)
			e.poolsMu.Unlock()
			close(done)
		}()
		defer recovery.Log(e.log, "close branch pool")
		_ = p.close(context.Background())
	}()
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
			e.safeTick(time.Now())
		}
	}
}

// safeTick runs tick, logging a panic instead of ending the process. The
// next tick runs as usual.
func (e *Engine) safeTick(now time.Time) {
	defer recovery.Log(e.log, "janitor")
	e.tick(now)
}

// tick runs the periodic work: idle connections, expired branches,
// scheduled snapshots and the trash.
func (e *Engine) tick(now time.Time) {
	e.closeIdle(now.Add(-e.cfg.IdleConnTimeout))
	if e.refused != nil {
		return
	}
	// Cancelled when the engine closes, so shutdown never waits on a long
	// scheduled snapshot.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	go func() {
		select {
		case <-e.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	e.deleteExpiredBranches(ctx)
	// Revoked tokens are kept for the trash retention, then cleared, so the
	// tokens table can't grow without bound.
	if _, err := e.meta.PurgeRevokedTokens(ctx, now.Add(-e.cfg.TrashRetention)); err != nil {
		e.log.Error("purge revoked tokens", "err", err)
	}
	e.runSchedules(ctx, now)
	e.purgeTrash(ctx, now)
	e.purgeWorkspaces(ctx, now)
	e.objectHousekeeping(ctx, now)
}

// removeStaleTemp deletes files a crash left mid-copy: the tmp folder's
// contents and *.tmp-* files next to branches and snapshots. They were never
// recorded in meta.db, so nothing refers to them.
func (e *Engine) removeStaleTemp() {
	if entries, err := os.ReadDir(e.layout.TmpDir()); err == nil {
		for _, en := range entries {
			_ = os.RemoveAll(filepath.Join(e.layout.TmpDir(), en.Name()))
		}
	}
	for _, root := range []string{e.layout.DBsDir(), e.layout.SnapshotsDir()} {
		matches, _ := filepath.Glob(filepath.Join(root, "*", "*.tmp-*"))
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}
}

// closeIdle closes pools unused since before cutoff.
func (e *Engine) closeIdle(cutoff time.Time) {
	e.poolsMu.Lock()
	defer e.poolsMu.Unlock()
	for id, p := range e.pools {
		if ok, last := evictable(p); ok && last.Before(cutoff) {
			e.closeLocked(id)
		}
	}
}

// scanUsage measures every live branch file at start.
func (e *Engine) scanUsage(ctx context.Context) error {
	workspaces, err := e.meta.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, w := range workspaces {
		branches, err := e.meta.ListWorkspaceBranches(ctx, w.ID)
		if err != nil {
			return err
		}
		for _, b := range branches {
			e.recordSize(w.ID, b.ID, fileBytes(e.layout.BranchFile(b.DatabaseID, b.ID)))
		}
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

// recordSize sets what a branch takes on disk, counted for the instance and
// for the workspace it is in.
func (e *Engine) recordSize(ws, branchID string, n int64) {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	e.forgetLocked(branchID)
	e.sizes[branchID], e.owner[branchID] = n, ws
	e.total += n
	e.wsTotal[ws] += n
}

func (e *Engine) forgetSize(branchID string) {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	e.forgetLocked(branchID)
}

func (e *Engine) forgetLocked(branchID string) {
	n, ok := e.sizes[branchID]
	if !ok {
		return
	}
	ws := e.owner[branchID]
	e.total -= n
	if e.wsTotal[ws] -= n; e.wsTotal[ws] <= 0 {
		delete(e.wsTotal, ws)
	}
	delete(e.sizes, branchID)
	delete(e.owner, branchID)
}

// forgetWorkspace drops every branch size counted for a workspace: when it
// goes to the trash, and again when it is purged, so nothing recorded late
// stays counted against the instance.
func (e *Engine) forgetWorkspace(ws string) {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	for id, owner := range e.owner {
		if owner == ws {
			e.forgetLocked(id)
		}
	}
	delete(e.wsTotal, ws)
}

// WorkspaceBytes is the space the branches of a workspace's live databases
// take.
func (e *Engine) WorkspaceBytes(ws string) int64 {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	return e.wsTotal[ws]
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

// CreateDatabase creates a database in a workspace, with an empty default
// branch, main.
func (e *Engine) CreateDatabase(ctx context.Context, ws, name string) (meta.Database, error) {
	if e.refused != nil {
		return meta.Database{}, e.refused
	}
	if !ids.ValidName(name) {
		return meta.Database{}, apierr.Newf(apierr.InvalidName, "invalid database name %q", name)
	}
	if _, err := e.meta.GetDatabase(ctx, ws, name); err == nil {
		return meta.Database{}, apierr.Newf(apierr.NameTaken, "a database named %q already exists", name)
	}
	n, err := e.meta.CountDatabases(ctx)
	if err != nil {
		return meta.Database{}, err
	}
	if n >= e.cfg.MaxDatabases {
		return meta.Database{}, apierr.New(apierr.LimitReached, "the instance can't hold more databases")
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
	d, _, err := e.meta.CreateDatabase(ctx, ws, name, dbID, branchID, e.cfg.MaxDatabases)
	if err != nil {
		e.dropPool(branchID)
		_ = os.RemoveAll(e.layout.DatabaseDir(dbID))
		return meta.Database{}, err
	}
	e.recordSize(ws, branchID, fileBytes(path))
	return d, nil
}

// dropPool closes and forgets a branch's pool, and waits for one that was
// already closing, so the caller can touch the file afterwards.
func (e *Engine) dropPool(branchID string) {
	e.poolsMu.Lock()
	p, ok := e.pools[branchID]
	delete(e.pools, branchID)
	ch := e.closing[branchID]
	e.poolsMu.Unlock()
	if ok {
		_ = p.close(context.Background())
	}
	if ch != nil {
		<-ch
	}
}

// target is a resolved database and branch.
type target struct {
	db     meta.Database
	branch meta.Branch
	path   string
}

// resolve finds a database of a workspace and a branch of it, by name or id.
// An empty branch means the database's default branch.
func (e *Engine) resolve(ctx context.Context, ws, dbRef, branchRef string) (target, error) {
	if e.refused != nil {
		return target{}, e.refused
	}
	d, err := e.meta.GetDatabase(ctx, ws, dbRef)
	if err != nil {
		return target{}, err
	}
	return e.resolveIn(ctx, d, branchRef)
}

// resolveByID finds a database and branch by their ids alone, for the
// engine's own jobs, which start from ids in meta.db and not from a request.
func (e *Engine) resolveByID(ctx context.Context, dbID, branchID string) (target, error) {
	if e.refused != nil {
		return target{}, e.refused
	}
	d, err := e.meta.DatabaseByID(ctx, dbID)
	if err != nil {
		return target{}, err
	}
	return e.resolveIn(ctx, d, branchID)
}

func (e *Engine) resolveIn(ctx context.Context, d meta.Database, branchRef string) (target, error) {
	if branchRef == "" {
		branchRef = d.DefaultBranchID
	}
	b, err := e.meta.GetBranch(ctx, d.ID, branchRef)
	if err != nil {
		return target{}, err
	}
	return target{db: d, branch: b, path: e.layout.BranchFile(d.ID, b.ID)}, nil
}
