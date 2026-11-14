package engine

import (
	"context"
	"os"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// Workspaces (PLAN 17, ADR 0008). Every database and bucket is in one, and
// every function here that takes a database or bucket name takes its
// workspace too: nothing resolves a name without one.

// DefaultWorkspace returns the instance's default workspace, where callers
// with no workspace of their own act.
func (e *Engine) DefaultWorkspace(ctx context.Context) (meta.Workspace, error) {
	if e.refused != nil {
		return meta.Workspace{}, e.refused
	}
	return e.meta.DefaultWorkspace(ctx)
}

// GetWorkspace finds a live workspace by name or id.
func (e *Engine) GetWorkspace(ctx context.Context, ref string) (meta.Workspace, error) {
	if e.refused != nil {
		return meta.Workspace{}, e.refused
	}
	return e.meta.GetWorkspace(ctx, ref)
}

// ListWorkspaces returns live workspaces by name.
func (e *Engine) ListWorkspaces(ctx context.Context) ([]meta.Workspace, error) {
	if e.refused != nil {
		return nil, e.refused
	}
	return e.meta.ListWorkspaces(ctx)
}

// NewWorkspaceLimits are a new workspace's limits. A nil field takes the
// instance's setting for new workspaces.
type NewWorkspaceLimits struct {
	MaxDatabases     *int
	MaxBuckets       *int
	MaxDatabaseBytes *int64
	MaxObjectBytes   *int64
}

// CreateWorkspace creates an empty workspace.
func (e *Engine) CreateWorkspace(ctx context.Context, name string, l NewWorkspaceLimits) (meta.Workspace, error) {
	if e.refused != nil {
		return meta.Workspace{}, e.refused
	}
	if !ids.ValidName(name) {
		return meta.Workspace{}, apierr.Newf(apierr.InvalidName, "invalid workspace name %q", name)
	}
	limits := meta.WorkspaceLimits{
		MaxDatabases:     e.cfg.WorkspaceMaxDatabases,
		MaxBuckets:       e.cfg.WorkspaceMaxBuckets,
		MaxDatabaseBytes: e.cfg.WorkspaceMaxDatabaseBytes,
		MaxObjectBytes:   e.cfg.WorkspaceMaxObjectBytes,
	}
	if l.MaxDatabases != nil {
		limits.MaxDatabases = *l.MaxDatabases
	}
	if l.MaxBuckets != nil {
		limits.MaxBuckets = *l.MaxBuckets
	}
	if l.MaxDatabaseBytes != nil {
		limits.MaxDatabaseBytes = *l.MaxDatabaseBytes
	}
	if l.MaxObjectBytes != nil {
		limits.MaxObjectBytes = *l.MaxObjectBytes
	}
	return e.meta.CreateWorkspace(ctx, ids.New(ids.Workspace), name, limits, e.cfg.MaxWorkspaces)
}

// UpdateWorkspace renames a workspace or changes its limits.
func (e *Engine) UpdateWorkspace(ctx context.Context, id string, u meta.WorkspaceUpdate) (meta.Workspace, error) {
	if e.refused != nil {
		return meta.Workspace{}, e.refused
	}
	return e.meta.UpdateWorkspace(ctx, id, u)
}

// WorkspaceUsage is what a workspace holds right now.
type WorkspaceUsage struct {
	Databases     int // counting the trash
	Buckets       int
	Tokens        int   // live
	DatabaseBytes int64 // branch files of live databases
	ObjectBytes   int64 // objects plus multipart parts in progress
}

// WorkspaceUsage measures what a workspace holds.
func (e *Engine) WorkspaceUsage(ctx context.Context, wsID string) (WorkspaceUsage, error) {
	if e.refused != nil {
		return WorkspaceUsage{}, e.refused
	}
	c, err := e.meta.CountWorkspace(ctx, wsID)
	if err != nil {
		return WorkspaceUsage{}, err
	}
	u := WorkspaceUsage{Databases: c.Databases, Buckets: c.Buckets, Tokens: c.Tokens, ObjectBytes: c.ObjectBytes}
	u.DatabaseBytes = e.WorkspaceBytes(wsID)
	return u, nil
}

// ---- trash, restore, purge (PLAN 17.6)

// DeleteWorkspace moves a workspace to the trash. At once nothing in it
// resolves any more: its tokens stop authenticating, its databases and
// buckets aren't found, open connections and transactions are closed. It
// can be restored, with everything in it, until the trash retention passes.
func (e *Engine) DeleteWorkspace(ctx context.Context, id string) (meta.Workspace, error) {
	if e.refused != nil {
		return meta.Workspace{}, e.refused
	}
	e.trashMu.Lock()
	defer e.trashMu.Unlock()
	w, err := e.meta.GetWorkspace(ctx, id)
	if err != nil {
		return meta.Workspace{}, err
	}
	if w.Default {
		return meta.Workspace{}, meta.ErrDefaultWorkspace
	}
	branches, err := e.meta.ListWorkspaceBranches(ctx, w.ID)
	if err != nil {
		return meta.Workspace{}, err
	}
	// Hold every branch alone so no request is mid-flight when the
	// workspace stops resolving. A request waiting on a gate looks its
	// database up again once it gets in, and no longer finds it.
	ctx2, cancel := context.WithTimeout(ctx, maintenanceTimeout)
	defer cancel()
	var held []*gate
	defer func() {
		for _, g := range held {
			g.release()
		}
	}()
	for _, b := range branches {
		g := e.gates.get(b.ID)
		if err := g.exclusiveWith(ctx2, func() { e.abortTxs(b.ID) }); err != nil {
			return meta.Workspace{}, apierr.New(apierr.Busy, "timed out waiting for requests on the workspace's databases to finish")
		}
		held = append(held, g)
	}
	if err := e.meta.SoftDeleteWorkspace(ctx, w.ID, time.Now().Add(e.cfg.TrashRetention)); err != nil {
		return meta.Workspace{}, err
	}
	for _, b := range branches {
		e.dropPool(b.ID)
		e.forgetSize(b.ID)
	}
	return e.meta.GetDeletedWorkspace(ctx, w.ID)
}

// ListDeletedWorkspaces returns workspaces in the trash.
func (e *Engine) ListDeletedWorkspaces(ctx context.Context) ([]meta.Workspace, error) {
	if e.refused != nil {
		return nil, e.refused
	}
	return e.meta.ListDeletedWorkspaces(ctx)
}

// GetDeletedWorkspace finds a workspace in the trash by name or id.
func (e *Engine) GetDeletedWorkspace(ctx context.Context, ref string) (meta.Workspace, error) {
	if e.refused != nil {
		return meta.Workspace{}, e.refused
	}
	return e.meta.GetDeletedWorkspace(ctx, ref)
}

// RestoreWorkspace takes a workspace out of the trash with everything in
// it, tokens included. An empty name keeps the old one, which fails if
// another workspace took it meanwhile.
func (e *Engine) RestoreWorkspace(ctx context.Context, ref, name string) (meta.Workspace, error) {
	if e.refused != nil {
		return meta.Workspace{}, e.refused
	}
	e.trashMu.Lock()
	defer e.trashMu.Unlock()
	w, err := e.meta.GetDeletedWorkspace(ctx, ref)
	if err != nil {
		return meta.Workspace{}, err
	}
	if name == "" {
		name = w.Name
	}
	if _, err := e.meta.GetWorkspace(ctx, name); err == nil {
		return meta.Workspace{}, apierr.Newf(apierr.NameTaken, "a workspace named %q already exists; restore it under another name", name)
	}
	branches, err := e.meta.ListWorkspaceBranches(ctx, w.ID)
	if err != nil {
		return meta.Workspace{}, err
	}
	// Its branch files count against the instance quota again.
	sizes := make([]int64, len(branches))
	var need int64
	for i, b := range branches {
		sizes[i] = fileBytes(e.layout.BranchFile(b.DatabaseID, b.ID))
		need += sizes[i]
	}
	e.usageMu.Lock()
	if e.total+e.reserved+need > e.cfg.MaxInstanceBytes {
		e.usageMu.Unlock()
		return meta.Workspace{}, apierr.New(apierr.InstanceQuotaExceeded, "restoring the workspace would pass the instance's size quota")
	}
	e.reserved += need
	e.usageMu.Unlock()
	defer func() {
		e.usageMu.Lock()
		e.reserved -= need
		e.usageMu.Unlock()
	}()
	restored, err := e.meta.RestoreWorkspace(ctx, w.ID, name)
	if err != nil {
		return meta.Workspace{}, err
	}
	for i, b := range branches {
		e.recordSize(w.ID, b.ID, sizes[i])
	}
	return restored, nil
}

// purgeWorkspaces removes workspaces whose trash retention has passed.
func (e *Engine) purgeWorkspaces(ctx context.Context, now time.Time) {
	due, err := e.meta.WorkspacesToPurge(ctx, now)
	if err != nil {
		e.log.Error("list workspaces to purge", "err", err)
		return
	}
	for _, w := range due {
		e.purgeWorkspace(ctx, w.ID)
	}
}

// purgeWorkspace removes a trashed workspace for good: records first, in a
// transaction that confirms it is still trashed, then its database files.
// Object files go with the object sweep once their rows are gone.
func (e *Engine) purgeWorkspace(ctx context.Context, id string) {
	e.trashMu.Lock()
	defer e.trashMu.Unlock()
	gone, purged, err := e.meta.PurgeWorkspace(ctx, id)
	if err != nil {
		e.log.Error("purge workspace records", "workspace", id, "err", err)
		return
	}
	if !purged {
		return // restored meanwhile
	}
	for _, db := range gone.Databases {
		for _, dir := range []string{e.layout.DatabaseDir(db), e.layout.SnapshotDir(db), e.layout.TrashEntry(db)} {
			if err := os.RemoveAll(dir); err != nil {
				e.log.Error("purge workspace files", "workspace", id, "path", dir, "err", err)
			}
		}
	}
	for _, up := range gone.Uploads {
		_ = os.RemoveAll(e.layout.UploadDir(up))
	}
}

// ---- moving between workspaces

// MoveDatabase puts a database of one workspace into another. Every token
// that reached it is revoked, and its open transactions are ended.
func (e *Engine) MoveDatabase(ctx context.Context, fromWS, dbRef, toRef string) (meta.Database, error) {
	if e.refused != nil {
		return meta.Database{}, e.refused
	}
	e.trashMu.Lock()
	defer e.trashMu.Unlock()
	d, err := e.meta.GetDatabase(ctx, fromWS, dbRef)
	if err != nil {
		return meta.Database{}, err
	}
	to, err := e.meta.GetWorkspace(ctx, toRef)
	if err != nil {
		return meta.Database{}, err
	}
	if to.ID == d.WorkspaceID {
		return meta.Database{}, apierr.New(apierr.BadRequest, "the database is already in that workspace")
	}
	branches, err := e.meta.ListBranches(ctx, d.ID)
	if err != nil {
		return meta.Database{}, err
	}
	var size int64
	for _, b := range branches {
		size += e.BranchBytes(b.ID)
	}
	// Its branch files count against the new workspace's own limit. The
	// instance's total doesn't change.
	e.usageMu.Lock()
	if l := to.Limits.MaxDatabaseBytes; l > 0 && e.wsTotal[to.ID]+e.wsReserved[to.ID]+size > l {
		e.usageMu.Unlock()
		return meta.Database{}, apierr.Newf(apierr.WorkspaceQuotaExceeded, "the database's %d bytes would pass the workspace's %d byte limit on its databases", size, l)
	}
	e.wsReserved[to.ID] += size
	e.usageMu.Unlock()
	defer e.releaseWorkspaceBytes(to.ID, size)

	moved, err := e.meta.MoveDatabase(ctx, d.ID, d.WorkspaceID, to.ID)
	if err != nil {
		return meta.Database{}, err
	}
	for _, b := range branches {
		// A transaction a revoked token left open would hold the writer
		// until its timeout.
		e.abortTxs(b.ID)
		e.recordSize(to.ID, b.ID, e.BranchBytes(b.ID))
	}
	return moved, nil
}

func (e *Engine) releaseWorkspaceBytes(ws string, n int64) {
	e.usageMu.Lock()
	if e.wsReserved[ws] -= n; e.wsReserved[ws] <= 0 {
		delete(e.wsReserved, ws)
	}
	e.usageMu.Unlock()
}

// MoveBucket puts a bucket of one workspace into another. Every token that
// reached it is revoked, with its access key and the URLs it signed.
func (e *Engine) MoveBucket(ctx context.Context, fromWS, bucketRef, toRef string) (meta.Bucket, error) {
	if e.refused != nil {
		return meta.Bucket{}, e.refused
	}
	e.trashMu.Lock()
	defer e.trashMu.Unlock()
	b, err := e.meta.GetBucket(ctx, fromWS, bucketRef)
	if err != nil {
		return meta.Bucket{}, err
	}
	to, err := e.meta.GetWorkspace(ctx, toRef)
	if err != nil {
		return meta.Bucket{}, err
	}
	if to.ID == b.WorkspaceID {
		return meta.Bucket{}, apierr.New(apierr.BadRequest, "the bucket is already in that workspace")
	}
	// The usage lock for objects, so an upload in flight can't be counted
	// against the old workspace while the bucket lands in the new one.
	e.objMu.Lock()
	defer e.objMu.Unlock()
	return e.meta.MoveBucket(ctx, b.ID, b.WorkspaceID, to.ID)
}
