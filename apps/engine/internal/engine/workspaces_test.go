package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// filled makes a workspace holding a live database with a branch and a
// snapshot, a database in the trash, a bucket with an object and an upload
// in progress, and a token.
type filledWorkspace struct {
	ws              meta.Workspace
	live, trashed   meta.Database
	snapshot        meta.Snapshot
	bucket          meta.Bucket
	upload          meta.Upload
	blob            string
	holds           string // what its object holds
	token           string
	branchFile, dev string
}

func fill(t *testing.T, e *Engine, name string) filledWorkspace {
	t.Helper()
	w, err := e.CreateWorkspace(bg, name, NewWorkspaceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	f := filledWorkspace{ws: w, token: "tok_" + strings.Repeat(name[:1], 20), holds: "object of " + name}
	if f.live, err = e.CreateDatabase(bg, w.ID, "notes"); err != nil {
		t.Fatal(err)
	}
	tg := Target{Workspace: w.ID, Database: "notes"}
	for _, sql := range []string{"CREATE TABLE t(v)", "INSERT INTO t VALUES ('kept in " + name + "')"} {
		if _, err := e.Query(bg, tg, Write, stmt(sql)); err != nil {
			t.Fatal(err)
		}
	}
	dev, err := e.CreateBranch(bg, w.ID, "notes", "dev", BranchSource{}, NewBranchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.dev = dev.ID
	f.branchFile = e.layout.BranchFile(f.live.ID, f.live.DefaultBranchID)
	if f.snapshot, err = e.CreateSnapshot(bg, tg, "s"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SetSchedule(bg, tg, ScheduleOptions{Every: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if f.trashed, err = e.CreateDatabase(bg, w.ID, "old"); err != nil {
		t.Fatal(err)
	}
	if f.trashed, err = e.DeleteDatabase(bg, w.ID, "old"); err != nil {
		t.Fatal(err)
	}
	if f.bucket, err = e.CreateBucket(bg, w.ID, "files"); err != nil {
		t.Fatal(err)
	}
	o := mustPut(t, e, f.bucket.ID, "doc.txt", f.holds)
	f.blob = o.BlobID
	if f.upload, err = e.CreateUpload(bg, w.ID, f.bucket.ID, "big", ObjectAttrs{}, ""); err != nil {
		t.Fatal(err)
	}
	// A part, so the upload has a folder on disk.
	if _, err := e.UploadPart(bg, UploadPartInput{Workspace: w.ID, UploadID: f.upload.ID, BucketID: f.bucket.ID, Key: "big", Number: 1, Size: 4, Body: strings.NewReader("part")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.meta.CreateToken(bg, meta.Token{ID: f.token, Name: "t", SecretHash: []byte{1}, WorkspaceID: w.ID, Scope: "grants", Access: "admin",
		Grants: []meta.Grant{{DatabaseID: f.live.ID, Access: "admin"}, {BucketID: f.bucket.ID, Access: "read"}}}, 0); err != nil {
		t.Fatal(err)
	}
	return f
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// files are the paths a filled workspace keeps on disk.
func (f filledWorkspace) files(e *Engine) map[string]string {
	return map[string]string{
		"database folder": e.layout.DatabaseDir(f.live.ID),
		"snapshot folder": e.layout.SnapshotDir(f.live.ID),
		"trashed folder":  e.layout.TrashEntry(f.trashed.ID),
		"object file":     e.layout.BlobFile(f.bucket.ID, f.blob),
		"upload folder":   e.layout.UploadDir(f.upload.ID),
	}
}

func (f filledWorkspace) whole(t *testing.T, e *Engine) {
	t.Helper()
	for what, path := range f.files(e) {
		// Its own trashed database is purged on its own retention, which
		// the purge test runs past.
		if what != "trashed folder" && !exists(path) {
			t.Errorf("%s: its %s is gone", f.ws.Name, what)
		}
	}
	r, err := e.Query(bg, Target{Workspace: f.ws.ID, Database: "notes"}, Read, stmt("SELECT v FROM t"))
	if err != nil || len(r.Rows) != 1 {
		t.Errorf("%s: rows %v %v", f.ws.Name, r, err)
	}
	if got := read(t, e, f.bucket.ID, "doc.txt"); got != f.holds {
		t.Errorf("%s: object %q", f.ws.Name, got)
	}
	if tok, err := e.meta.GetToken(bg, f.token); err != nil || tok.RevokedAt != nil || len(tok.Grants) != 2 {
		t.Errorf("%s: token %+v %v", f.ws.Name, tok, err)
	}
	if _, ok, err := e.GetSchedule(bg, Target{Workspace: f.ws.ID, Database: "notes"}); err != nil || !ok {
		t.Errorf("%s: schedule %v %v", f.ws.Name, ok, err)
	}
}

// Deleting a workspace keeps every file and record until the retention
// passes, through a restart too, and restoring brings it back whole. While
// it is in the trash nothing in it resolves and its jobs pause.
func TestWorkspaceTrashAndRestore(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	open := func() *Engine {
		e, err := Open(bg, cfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		e.freeBytes = func(string) (int64, error) { return 1 << 50, nil }
		return e
	}
	e := open()
	gone, kept := fill(t, e, "gone"), fill(t, e, "kept")
	before := e.TotalBytes()

	// An open transaction is ended by the delete.
	tx, err := e.BeginTx(bg, Target{Workspace: gone.ws.ID, Database: "notes"}, Write, TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DeleteWorkspace(bg, ws(e)); apierr.As(err).Code != apierr.Forbidden {
		t.Fatalf("the default workspace was deleted: %v", err)
	}
	deleted, err := e.DeleteWorkspace(bg, gone.ws.ID)
	if err != nil || deleted.DeletedAt == nil || deleted.PurgeAfter == nil {
		t.Fatalf("delete: %+v %v", deleted, err)
	}
	if !tx.Done() {
		t.Fatal("the delete left a transaction open")
	}
	if _, err := e.DeleteWorkspace(bg, gone.ws.ID); apierr.As(err).Code != apierr.WorkspaceNotFound {
		t.Fatalf("deleting it twice: %v", err)
	}

	trashedState := func(e *Engine) {
		t.Helper()
		// Nothing resolves, by name or by id, for a request or a job.
		if _, err := e.GetWorkspace(bg, gone.ws.ID); apierr.As(err).Code != apierr.WorkspaceNotFound {
			t.Errorf("the workspace resolves: %v", err)
		}
		for _, ref := range []string{"notes", gone.live.ID} {
			if _, err := e.GetDatabase(bg, gone.ws.ID, ref); apierr.As(err).Code != apierr.DatabaseNotFound {
				t.Errorf("database %s resolves: %v", ref, err)
			}
			if _, err := e.Query(bg, Target{Workspace: gone.ws.ID, Database: ref}, Read, stmt("SELECT 1")); apierr.As(err).Code != apierr.DatabaseNotFound {
				t.Errorf("query on %s: %v", ref, err)
			}
		}
		for _, ref := range []string{"files", gone.bucket.ID} {
			if _, err := e.GetBucket(bg, gone.ws.ID, ref); apierr.As(err).Code != apierr.BucketNotFound {
				t.Errorf("bucket %s resolves: %v", ref, err)
			}
		}
		if _, err := e.meta.DatabaseByID(bg, gone.live.ID); err == nil {
			t.Error("the engine's own jobs still find the database")
		}
		// Every file is still there, and so is every record.
		for what, path := range gone.files(e) {
			if !exists(path) {
				t.Errorf("its %s was removed while in the trash", what)
			}
		}
		if tok, err := e.meta.GetToken(bg, gone.token); err != nil || tok.RevokedAt != nil || len(tok.Grants) != 2 {
			t.Errorf("its token: %+v %v", tok, err)
		}
		// It no longer counts against the instance, and the other
		// workspace is whole.
		if got := e.TotalBytes(); got >= before || got <= 0 {
			t.Errorf("bytes counted: %d, before %d", got, before)
		}
		if e.WorkspaceBytes(gone.ws.ID) != 0 || e.WorkspaceBytes(kept.ws.ID) <= 0 {
			t.Errorf("workspace bytes: gone %d, kept %d", e.WorkspaceBytes(gone.ws.ID), e.WorkspaceBytes(kept.ws.ID))
		}
		kept.whole(t, e)
	}
	trashedState(e)
	// Housekeeping takes nothing while the retention hasn't passed, and
	// takes no scheduled snapshot of a trashed workspace.
	snaps := func(e *Engine, f filledWorkspace) int {
		list, err := e.meta.ListSnapshots(bg, f.live.ID, f.live.DefaultBranchID)
		if err != nil {
			t.Fatal(err)
		}
		return len(list)
	}
	e.tick(time.Now().Add(2 * time.Minute))
	if got := snaps(e, gone); got != 1 {
		t.Errorf("a trashed workspace's schedule ran: %d snapshots", got)
	}
	if got := snaps(e, kept); got != 2 {
		t.Errorf("the live workspace's schedule didn't run: %d snapshots", got)
	}
	trashedState(e)

	// A restart sweeps files nothing owns. A trashed workspace still owns
	// its files.
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e = open()
	defer e.Close()
	trashedState(e)

	// The name is free while it is in the trash, so a restore may need
	// another one.
	if _, err := e.CreateWorkspace(bg, "gone", NewWorkspaceLimits{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RestoreWorkspace(bg, gone.ws.ID, ""); apierr.As(err).Code != apierr.NameTaken {
		t.Fatalf("restore under a taken name: %v", err)
	}
	back, err := e.RestoreWorkspace(bg, gone.ws.ID, "back")
	if err != nil || back.ID != gone.ws.ID || back.Name != "back" || back.DeletedAt != nil {
		t.Fatalf("restore: %+v %v", back, err)
	}
	gone.ws = back
	gone.whole(t, e)
	kept.whole(t, e)
	if e.WorkspaceBytes(back.ID) <= 0 {
		t.Fatal("the restored workspace's bytes aren't counted")
	}
	if _, err := e.RestoreWorkspace(bg, gone.ws.ID, ""); apierr.As(err).Code != apierr.WorkspaceNotFound {
		t.Fatalf("restoring a live workspace: %v", err)
	}
}

// After the retention a trashed workspace is purged: its database files
// and folders, its records and its tokens go, its object files go with the
// object sweep, and nothing of another workspace is touched.
func TestWorkspacePurge(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.TrashRetention = time.Hour; c.ObjectGCDelay = time.Millisecond })
	gone, kept := fill(t, e, "gone"), fill(t, e, "kept")
	if _, err := e.DeleteWorkspace(bg, gone.ws.ID); err != nil {
		t.Fatal(err)
	}
	// Before the retention: nothing.
	e.tick(time.Now().Add(30 * time.Minute))
	for what, path := range gone.files(e) {
		if !exists(path) {
			t.Fatalf("its %s was purged before the retention", what)
		}
	}
	// After it.
	e.tick(time.Now().Add(2 * time.Hour))
	for what, path := range gone.files(e) {
		if what == "object file" {
			continue // the sweep below
		}
		if exists(path) {
			t.Errorf("its %s survived the purge", what)
		}
	}
	time.Sleep(5 * time.Millisecond)
	if err := e.sweepObjects(bg, time.Now(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if exists(e.layout.BlobFile(gone.bucket.ID, gone.blob)) || exists(filepath.Dir(filepath.Dir(e.layout.BlobFile(gone.bucket.ID, gone.blob)))) {
		t.Error("its object files survived the sweep")
	}
	if _, err := e.meta.GetToken(bg, gone.token); err != meta.ErrNoToken {
		t.Errorf("its token survived: %v", err)
	}
	if _, err := e.GetDeletedWorkspace(bg, gone.ws.ID); apierr.As(err).Code != apierr.WorkspaceNotFound {
		t.Errorf("it is still in the trash: %v", err)
	}
	if _, err := e.RestoreWorkspace(bg, gone.ws.ID, ""); apierr.As(err).Code != apierr.WorkspaceNotFound {
		t.Errorf("a purged workspace was restored: %v", err)
	}
	// No record of it is left.
	if dbs, err := e.meta.ListDatabases(bg, gone.ws.ID); err != nil || len(dbs) != 0 {
		t.Errorf("databases left: %v %v", dbs, err)
	}
	if dbs, err := e.meta.ListTrash(bg, gone.ws.ID); err != nil || len(dbs) != 0 {
		t.Errorf("trashed databases left: %v %v", dbs, err)
	}
	if bks, err := e.meta.ListBuckets(bg, gone.ws.ID); err != nil || len(bks) != 0 {
		t.Errorf("buckets left: %v %v", bks, err)
	}
	if toks, err := e.meta.ListTokens(bg, gone.ws.ID, "", "", 10); err != nil || len(toks) != 0 {
		t.Errorf("tokens left: %v %v", toks, err)
	}
	if brs, err := e.meta.ListBranches(bg, gone.live.ID); err != nil || len(brs) != 0 {
		t.Errorf("branches left: %v %v", brs, err)
	}
	if sns, err := e.meta.ListSnapshots(bg, gone.live.ID, gone.live.DefaultBranchID); err != nil || len(sns) != 0 {
		t.Errorf("snapshots left: %v %v", sns, err)
	}
	if _, ok, err := e.meta.GetSchedule(bg, gone.live.DefaultBranchID); err != nil || ok {
		t.Errorf("a schedule is left: %v %v", ok, err)
	}
	if _, err := e.meta.GetObject(bg, gone.bucket.ID, "doc.txt"); apierr.As(err).Code != apierr.ObjectNotFound {
		t.Errorf("an object row is left: %v", err)
	}
	if _, err := e.meta.GetUpload(bg, gone.upload.ID, gone.bucket.ID, "big"); apierr.As(err).Code != apierr.UploadNotFound {
		t.Errorf("an upload row is left: %v", err)
	}
	if _, err := e.meta.BucketByID(bg, gone.bucket.ID); apierr.As(err).Code != apierr.BucketNotFound {
		t.Errorf("the bucket row is left: %v", err)
	}
	// The other workspace, with the same names, and the default one.
	kept.whole(t, e)
	if _, err := e.DefaultWorkspace(bg); err != nil {
		t.Fatal(err)
	}
	// A workspace restored before the retention isn't purged.
	again := fill(t, e, "again")
	if _, err := e.DeleteWorkspace(bg, again.ws.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RestoreWorkspace(bg, again.ws.ID, ""); err != nil {
		t.Fatal(err)
	}
	e.tick(time.Now().Add(2 * time.Hour))
	again.whole(t, e)
}

// Each workspace's own limit on the bytes of its databases, at the engine:
// writes, branch copies and moves are counted per workspace.
func TestWorkspaceDatabaseBytes(t *testing.T) {
	e := newEngine(t, nil)
	limit := int64(400_000)
	tight, err := e.CreateWorkspace(bg, "tight", NewWorkspaceLimits{MaxDatabaseBytes: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.CreateDatabase(bg, tight.ID, "app"); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, e, "app") // the default workspace: no limit of its own
	tg, def := Target{Workspace: tight.ID, Database: "app"}, Target{Workspace: ws(e), Database: "app"}
	for _, x := range []Target{tg, def} {
		if _, err := e.Query(bg, x, Write, stmt("CREATE TABLE t(v)")); err != nil {
			t.Fatal(err)
		}
	}
	big := stmt("INSERT INTO t VALUES (zeroblob(300000))")
	if _, err := e.Query(bg, tg, Write, big); apierr.As(err).Code != apierr.WorkspaceQuotaExceeded {
		t.Fatalf("a write past the workspace's limit: %v", err)
	}
	if _, err := e.Query(bg, def, Write, big); err != nil {
		t.Fatalf("the same write in a workspace without a limit: %v", err)
	}
	if _, err := e.Query(bg, tg, Write, stmt("INSERT INTO t VALUES (zeroblob(150000))")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CreateBranch(bg, tight.ID, "app", "copy", BranchSource{}, NewBranchOptions{}); apierr.As(err).Code != apierr.WorkspaceQuotaExceeded {
		t.Fatalf("a branch copy past the workspace's limit: %v", err)
	}
	// Reads and deletes keep working at the limit.
	if _, err := e.Query(bg, tg, Read, stmt("SELECT count(*) FROM t")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Query(bg, tg, Write, stmt("DELETE FROM t")); err != nil {
		t.Fatalf("a delete at the limit: %v", err)
	}
	// What is counted for the workspace is what its branches take, and the
	// instance's total is the sum.
	if got := e.WorkspaceBytes(tight.ID); got <= 0 || got > limit {
		t.Fatalf("workspace bytes %d", got)
	}
	if e.TotalBytes() != e.WorkspaceBytes(tight.ID)+e.WorkspaceBytes(ws(e)) {
		t.Fatalf("total %d isn't the workspaces' sum %d + %d", e.TotalBytes(), e.WorkspaceBytes(tight.ID), e.WorkspaceBytes(ws(e)))
	}
	// A move into the tight workspace is refused for the same limit, and
	// one out of it frees the room.
	if _, err := e.MoveDatabase(bg, ws(e), "app", "tight"); apierr.As(err).Code != apierr.NameTaken && apierr.As(err).Code != apierr.WorkspaceQuotaExceeded {
		t.Fatalf("a move past the limit: %v", err)
	}
	if _, err := e.RenameDatabase(bg, tight.ID, "app", "moving"); err != nil {
		t.Fatal(err)
	}
	total := e.TotalBytes()
	if _, err := e.MoveDatabase(bg, tight.ID, "moving", "default"); err != nil {
		t.Fatal(err)
	}
	if e.WorkspaceBytes(tight.ID) != 0 || e.TotalBytes() != total || e.TotalBytes() != e.WorkspaceBytes(ws(e)) {
		t.Fatalf("after the move: tight %d, default %d, total %d (was %d)", e.WorkspaceBytes(tight.ID), e.WorkspaceBytes(ws(e)), e.TotalBytes(), total)
	}
}
