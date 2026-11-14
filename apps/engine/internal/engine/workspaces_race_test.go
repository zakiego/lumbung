package engine

// What is in flight when a workspace is deleted, or a database or bucket is
// moved, must not land afterwards (PLAN 17.6). Each test holds a request at
// the point where it waits, makes the change, lets the request go on, and
// checks that it found nothing and left nothing.

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// waiting starts a branch copy, a snapshot and an export on a branch whose
// gate the caller holds, so all three wait where a delete or a move would
// make them wait. It returns their errors, once the caller lets them go,
// and what the export wrote.
func waiting(t *testing.T, e *Engine, tg Target) (wait func() []error, export *bytes.Buffer) {
	t.Helper()
	results := make(chan error, 3)
	export = &bytes.Buffer{}
	go func() {
		_, err := e.CreateBranch(bg, tg.Workspace, tg.Database, "late", BranchSource{}, NewBranchOptions{})
		results <- err
	}()
	go func() {
		_, err := e.CreateSnapshot(bg, tg, "late")
		results <- err
	}()
	go func() { results <- e.ExportDB(bg, tg, export) }()
	time.Sleep(100 * time.Millisecond) // let them reach the gate
	return func() []error {
		var out []error
		for i := 0; i < 3; i++ {
			select {
			case err := <-results:
				out = append(out, err)
			case <-time.After(10 * time.Second):
				t.Fatal("a waiting copy never finished")
			}
		}
		return out
	}, export
}

func TestCopiesWaitingOnADeleteOrMoveFindNothing(t *testing.T) {
	for name, change := range map[string]func(e *Engine, f, other filledWorkspace) error{
		"the workspace is deleted": func(e *Engine, f, _ filledWorkspace) error {
			return e.meta.SoftDeleteWorkspace(bg, f.ws.ID, time.Now().Add(time.Hour))
		},
		"the database is moved": func(e *Engine, f, other filledWorkspace) error {
			if _, err := e.meta.RenameDatabase(bg, f.live.ID, "moving"); err != nil {
				return err
			}
			_, err := e.meta.MoveDatabase(bg, f.live.ID, f.ws.ID, other.ws.ID)
			return err
		},
		"the database is deleted": func(e *Engine, f, _ filledWorkspace) error {
			return e.meta.SoftDeleteDatabase(bg, f.live.ID, time.Now().Add(time.Hour))
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEngine(t, nil)
			f, other := fill(t, e, "gone"), fill(t, e, "kept")
			tg := Target{Workspace: f.ws.ID, Database: f.live.ID}
			// Hold the branch as a delete or a move does while it waits for
			// requests to finish.
			g := e.gates.get(f.live.DefaultBranchID)
			if err := g.exclusiveWith(bg, func() {}); err != nil {
				t.Fatal(err)
			}
			wait, export := waiting(t, e, tg)
			if err := change(e, f, other); err != nil {
				t.Fatal(err)
			}
			g.release()
			for _, err := range wait() {
				if apierr.As(err).Code != apierr.DatabaseNotFound {
					t.Errorf("a copy that waited went on: %v", err)
				}
			}
			if export.Len() != 0 {
				t.Errorf("the export returned %d bytes of the database", export.Len())
			}
			branches, err := e.meta.ListBranches(bg, f.live.ID)
			if err != nil || len(branches) != 2 {
				t.Errorf("a branch was recorded: %v %v", branches, err)
			}
			snaps, err := e.meta.ListSnapshots(bg, f.live.ID, f.live.DefaultBranchID)
			if err != nil || len(snaps) != 1 {
				t.Errorf("a snapshot was recorded: %v %v", snaps, err)
			}
			// No file was left behind either: two branch files, one snapshot.
			for dir, want := range map[string]int{e.layout.DatabaseDir(f.live.ID): 2, e.layout.SnapshotDir(f.live.ID): 1} {
				entries, _ := os.ReadDir(dir)
				n := 0
				for _, en := range entries {
					if strings.HasSuffix(en.Name(), ".db") {
						n++
					}
				}
				if n != want {
					t.Errorf("%s holds %d database files, want %d", dir, n, want)
				}
			}
		})
	}
}

// A branch or snapshot is only recorded while its database is live in the
// workspace the caller found it in, whatever the engine checked earlier.
func TestRecordsNeedALiveDatabaseInTheirWorkspace(t *testing.T) {
	e := newEngine(t, nil)
	f, other := fill(t, e, "here"), fill(t, e, "there")
	branch := func(ws string) error {
		_, err := e.meta.CreateBranch(bg, meta.NewBranch{ID: "br_" + strings.Repeat("z", 20), DatabaseID: f.live.ID, WorkspaceID: ws, Name: "x"})
		return err
	}
	snap := func(ws string) error {
		_, err := e.meta.CreateSnapshot(bg, meta.Snapshot{ID: "sn_" + strings.Repeat("z", 20), DatabaseID: f.live.ID,
			BranchID: f.live.DefaultBranchID, BranchName: "main", Kind: meta.SnapshotManual}, ws)
		return err
	}
	for _, ws := range []string{other.ws.ID, "", "ws_" + strings.Repeat("n", 20)} {
		if err := branch(ws); apierr.As(err).Code != apierr.DatabaseNotFound {
			t.Errorf("a branch was recorded through workspace %q: %v", ws, err)
		}
		if err := snap(ws); apierr.As(err).Code != apierr.DatabaseNotFound {
			t.Errorf("a snapshot was recorded through workspace %q: %v", ws, err)
		}
	}
	if err := e.meta.SoftDeleteWorkspace(bg, f.ws.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := branch(f.ws.ID); apierr.As(err).Code != apierr.DatabaseNotFound {
		t.Errorf("a branch was recorded in a trashed workspace: %v", err)
	}
	if err := snap(f.ws.ID); apierr.As(err).Code != apierr.DatabaseNotFound {
		t.Errorf("a snapshot was recorded in a trashed workspace: %v", err)
	}
	if _, err := e.meta.RestoreWorkspace(bg, f.ws.ID, "here"); err != nil {
		t.Fatal(err)
	}
	if err := branch(f.ws.ID); err != nil {
		t.Errorf("a branch in a live workspace: %v", err)
	}
	if err := snap(f.ws.ID); err != nil {
		t.Errorf("a snapshot in a live workspace: %v", err)
	}
}

// slowUpload starts an upload whose body arrives in two halves. It returns
// once the first half was read, so the upload has claimed its room and is
// writing, and a function that sends the rest and returns the upload's
// error.
func slowUpload(t *testing.T, e *Engine, ws, bucketID, key string, size int) (finish func() error) {
	t.Helper()
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := e.PutObject(bg, PutObjectInput{Workspace: ws, BucketID: bucketID, Key: key, Size: int64(size), Body: pr})
		done <- err
	}()
	if _, err := pw.Write(bytes.Repeat([]byte("a"), size/2)); err != nil {
		t.Fatal(err)
	}
	return func() error {
		_, _ = pw.Write(bytes.Repeat([]byte("b"), size-size/2))
		pw.Close()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("the upload never finished")
			return nil
		}
	}
}

// nothingLeft fails if an upload that was refused left a row, a file or a
// claim behind.
func nothingLeft(t *testing.T, e *Engine, bucketID, key string, wantFiles int) {
	t.Helper()
	if _, err := e.meta.GetObject(bg, bucketID, key); apierr.As(err).Code != apierr.ObjectNotFound {
		t.Errorf("the refused upload was recorded: %v", err)
	}
	files := 0
	for path := range tree(t, e.layout.BucketDir(bucketID)) {
		if !strings.HasSuffix(path, "/") {
			files++
		}
	}
	if files != wantFiles {
		t.Errorf("the bucket's folder holds %d files, want %d", files, wantFiles)
	}
	if tmp, _ := os.ReadDir(e.layout.TmpDir()); len(tmp) != 0 {
		t.Errorf("temp files left: %d", len(tmp))
	}
	e.objMu.Lock()
	defer e.objMu.Unlock()
	if e.objReservedTotal != 0 || len(e.objReserved) != 0 || len(e.objReservedWS) != 0 {
		t.Errorf("claims left: total %d, by bucket %v, by workspace %v", e.objReservedTotal, e.objReserved, e.objReservedWS)
	}
}

func TestUploadInFlightDuringAMove(t *testing.T) {
	e := newEngine(t, nil)
	from, err := e.CreateWorkspace(bg, "from", NewWorkspaceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	limit := int64(500)
	to, err := e.CreateWorkspace(bg, "to", NewWorkspaceLimits{MaxObjectBytes: &limit})
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.CreateBucket(bg, from.ID, "files")
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, e, b.ID, "kept", "there before the move")

	// An upload of the old workspace is half-way when the bucket moves. It
	// is bigger than the new workspace's whole limit.
	finish := slowUpload(t, e, from.ID, b.ID, "late", 1000)
	moved, err := e.MoveBucket(bg, from.ID, "files", "to")
	if err != nil || moved.WorkspaceID != to.ID {
		t.Fatalf("move: %+v %v", moved, err)
	}
	if err := finish(); apierr.As(err).Code != apierr.BucketNotFound {
		t.Fatalf("an upload of the old workspace landed after the move: %v", err)
	}
	nothingLeft(t, e, b.ID, "late", 1)
	if u, err := e.WorkspaceUsage(bg, to.ID); err != nil || u.ObjectBytes != int64(len("there before the move")) {
		t.Fatalf("the new workspace's usage: %+v %v", u, err)
	}
	// The old workspace writes nothing to it any more, by any path.
	if _, err := e.PutObject(bg, PutObjectInput{Workspace: from.ID, BucketID: b.ID, Key: "x", Size: 1, Body: strings.NewReader("x")}); apierr.As(err).Code != apierr.BucketNotFound {
		t.Errorf("put through the old workspace: %v", err)
	}
	if _, err := e.DeleteObject(bg, from.ID, b.ID, "kept"); apierr.As(err).Code != apierr.BucketNotFound {
		t.Errorf("delete through the old workspace: %v", err)
	}
	if _, err := e.CreateUpload(bg, from.ID, b.ID, "big", ObjectAttrs{}, ""); apierr.As(err).Code != apierr.BucketNotFound {
		t.Errorf("an upload through the old workspace: %v", err)
	}
	if _, err := e.CopyObject(bg, CopyObjectInput{Workspace: from.ID, SrcBucketID: b.ID, SrcKey: "kept", BucketID: b.ID, Key: "copy"}); apierr.As(err).Code != apierr.BucketNotFound {
		t.Errorf("a copy through the old workspace: %v", err)
	}
	if got := read(t, e, b.ID, "kept"); got != "there before the move" {
		t.Errorf("the object that was there: %q", got)
	}
	// The new workspace does, under its own limit.
	if _, err := e.PutObject(bg, PutObjectInput{Workspace: to.ID, BucketID: b.ID, Key: "new", Size: 100, Body: strings.NewReader(strings.Repeat("n", 100))}); err != nil {
		t.Fatalf("put through the new workspace: %v", err)
	}
	if _, err := e.PutObject(bg, PutObjectInput{Workspace: to.ID, BucketID: b.ID, Key: "big", Size: 1000, Body: strings.NewReader(strings.Repeat("n", 1000))}); apierr.As(err).Code != apierr.WorkspaceQuotaExceeded {
		t.Fatalf("a put past the new workspace's limit: %v", err)
	}

	// A move counts what uploads running in the target have claimed. Here
	// 300 of the target's 500 bytes are claimed in flight and 121 are
	// stored, so a 200 byte bucket doesn't fit, though meta.db alone says
	// it would.
	other, err := e.CreateBucket(bg, from.ID, "more")
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, e, other.ID, "big", strings.Repeat("m", 200))
	finish = slowUpload(t, e, to.ID, b.ID, "claiming", 300)
	if _, err := e.MoveBucket(bg, from.ID, "more", "to"); apierr.As(err).Code != apierr.WorkspaceQuotaExceeded {
		t.Fatalf("a move past the target's limit, counting uploads in flight there: %v", err)
	}
	if err := finish(); err != nil {
		t.Fatalf("the upload in the target: %v", err)
	}
	if got, err := e.GetBucket(bg, from.ID, "more"); err != nil || got.WorkspaceID != from.ID {
		t.Fatalf("the refused move changed the bucket: %+v %v", got, err)
	}
}

func TestUploadInFlightDuringAWorkspaceDelete(t *testing.T) {
	e := newEngine(t, nil)
	f, kept := fill(t, e, "gone"), fill(t, e, "kept")
	finish := slowUpload(t, e, f.ws.ID, f.bucket.ID, "late", 1000)
	// A multipart part in flight too.
	pr, pw := io.Pipe()
	part := make(chan error, 1)
	go func() {
		_, err := e.UploadPart(bg, UploadPartInput{Workspace: f.ws.ID, UploadID: f.upload.ID, BucketID: f.bucket.ID, Key: "big", Number: 2, Size: 10, Body: pr})
		part <- err
	}()
	if _, err := pw.Write([]byte("12345")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DeleteWorkspace(bg, f.ws.ID); err != nil {
		t.Fatal(err)
	}
	if err := finish(); apierr.As(err).Code != apierr.BucketNotFound {
		t.Fatalf("an upload landed in a workspace in the trash: %v", err)
	}
	_, _ = pw.Write([]byte("67890"))
	pw.Close()
	if err := <-part; apierr.As(err).Code != apierr.BucketNotFound {
		t.Fatalf("a part landed in a workspace in the trash: %v", err)
	}
	nothingLeft(t, e, f.bucket.ID, "late", 1)
	if parts, err := e.meta.ListParts(bg, f.upload.ID, 0, 10); err != nil || len(parts) != 1 {
		t.Fatalf("parts recorded: %v %v", parts, err)
	}
	// Nothing else writes there either, and what it held is as it was for
	// a restore.
	if _, err := e.DeleteObject(bg, f.ws.ID, f.bucket.ID, "doc.txt"); apierr.As(err).Code != apierr.BucketNotFound {
		t.Errorf("a delete in a trashed workspace: %v", err)
	}
	if err := e.AbortUpload(bg, f.ws.ID, f.upload.ID, f.bucket.ID, "big"); apierr.As(err).Code != apierr.BucketNotFound {
		t.Errorf("an abort in a trashed workspace: %v", err)
	}
	if _, err := e.CompleteUpload(bg, f.ws.ID, f.upload.ID, f.bucket.ID, "big", []CompletedPart{{1, "x"}}, nil); apierr.As(err).Code != apierr.BucketNotFound && apierr.As(err).Code != apierr.InvalidPart {
		t.Errorf("a complete in a trashed workspace: %v", err)
	}
	if _, err := e.RestoreWorkspace(bg, f.ws.ID, ""); err != nil {
		t.Fatal(err)
	}
	f.whole(t, e)
	kept.whole(t, e)
}

// A move holds the database's branches alone, like a delete: a transaction
// of the old workspace is ended, and nothing of the old workspace commits
// afterwards or is counted against it.
func TestMoveDatabaseEndsWhatWasOpen(t *testing.T) {
	e := newEngine(t, nil)
	f, other := fill(t, e, "from"), fill(t, e, "to")
	if _, err := e.RenameDatabase(bg, f.ws.ID, "notes", "moving"); err != nil {
		t.Fatal(err)
	}
	old := Target{Workspace: f.ws.ID, Database: "moving"}
	tx, err := e.BeginTx(bg, old, Write, TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(bg, stmt("INSERT INTO t VALUES ('uncommitted')"), e.NewBudget()); err != nil {
		t.Fatal(err)
	}
	fromBefore, toBefore, total := e.WorkspaceBytes(f.ws.ID), e.WorkspaceBytes(other.ws.ID), e.TotalBytes()
	if _, err := e.MoveDatabase(bg, f.ws.ID, "moving", "to"); err != nil {
		t.Fatal(err)
	}
	if !tx.Done() {
		t.Fatal("the move left the old workspace's transaction open")
	}
	if err := tx.Commit(bg); err == nil {
		t.Fatal("the old workspace's transaction committed after the move")
	}
	if got := e.WorkspaceBytes(f.ws.ID); got != 0 {
		t.Fatalf("the old workspace is still charged %d bytes (was %d)", got, fromBefore)
	}
	if e.WorkspaceBytes(other.ws.ID) != toBefore+fromBefore || e.TotalBytes() != total {
		t.Fatalf("after the move: to %d (was %d + %d), total %d (was %d)", e.WorkspaceBytes(other.ws.ID), toBefore, fromBefore, e.TotalBytes(), total)
	}
	// The old workspace doesn't find it, and writes through the new one are
	// charged to the new one.
	if _, err := e.Query(bg, old, Write, stmt("INSERT INTO t VALUES ('late')")); apierr.As(err).Code != apierr.DatabaseNotFound {
		t.Fatalf("a write through the old workspace: %v", err)
	}
	if _, err := e.Query(bg, Target{Workspace: other.ws.ID, Database: "moving"}, Write, stmt("INSERT INTO t VALUES (zeroblob(100000))")); err != nil {
		t.Fatal(err)
	}
	if e.WorkspaceBytes(f.ws.ID) != 0 || e.WorkspaceBytes(other.ws.ID) <= toBefore+fromBefore {
		t.Fatalf("a write after the move was charged to the wrong workspace: from %d, to %d", e.WorkspaceBytes(f.ws.ID), e.WorkspaceBytes(other.ws.ID))
	}
	r, err := e.Query(bg, Target{Workspace: other.ws.ID, Database: "moving"}, Read, stmt("SELECT count(*) FROM t WHERE v = 'uncommitted'"))
	if err != nil || r.Rows[0][0].Int != 0 {
		t.Fatalf("the ended transaction's row: %v %v", r, err)
	}
}
