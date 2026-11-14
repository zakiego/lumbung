package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

func TestSnapshotsAndRestore(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	mustQuery(t, e, "app", Write, "INSERT INTO t VALUES (1)")
	sn, err := e.CreateSnapshot(bg, Target{Workspace: ws(e), Database: "app"}, "before change")
	if err != nil {
		t.Fatal(err)
	}
	if sn.Kind != meta.SnapshotManual || sn.Label != "before change" || sn.SizeBytes == 0 || sn.BranchName != "main" {
		t.Fatalf("snapshot: %+v", sn)
	}
	mustQuery(t, e, "app", Write, "DELETE FROM t")

	// Restore makes a new branch and leaves main alone.
	b, err := e.RestoreSnapshot(bg, ws(e), "app", sn.ID, "restored")
	if err != nil {
		t.Fatal(err)
	}
	if b.SourceSnapshotID != sn.ID {
		t.Fatalf("restored branch: %+v", b)
	}
	if v := scalar(t, mustOnBranch(t, e, "app", "restored", Read, "SELECT count(*) FROM t")); v.Int != 1 {
		t.Fatalf("restored rows %d", v.Int)
	}
	if v := scalar(t, mustQuery(t, e, "app", Read, "SELECT count(*) FROM t")); v.Int != 0 {
		t.Fatal("restore touched main")
	}
	// Reset of a restored branch copies the snapshot again.
	mustOnBranch(t, e, "app", "restored", Write, "INSERT INTO t VALUES (2)")
	if _, err := e.ResetBranch(bg, ws(e), "app", "restored"); err != nil {
		t.Fatal(err)
	}
	if v := scalar(t, mustOnBranch(t, e, "app", "restored", Read, "SELECT count(*) FROM t")); v.Int != 1 {
		t.Fatalf("reset restored rows %d", v.Int)
	}

	list, err := e.ListSnapshots(bg, Target{Workspace: ws(e), Database: "app"})
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	_, err = e.RestoreSnapshot(bg, ws(e), "app", "sn_doesnotexistxxxxxxxx", "x")
	wantCode(t, err, apierr.SnapshotNotFound, "missing snapshot")

	// A snapshot outlives its branch.
	if _, err := e.CreateBranch(bg, ws(e), "app", "tmp", BranchSource{}, NewBranchOptions{}); err != nil {
		t.Fatal(err)
	}
	sn2, err := e.CreateSnapshot(bg, Target{Workspace: ws(e), Database: "app", Branch: "tmp"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.DeleteBranch(bg, ws(e), "app", "tmp"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RestoreSnapshot(bg, ws(e), "app", sn2.ID, "back"); err != nil {
		t.Fatalf("restore from a deleted branch's snapshot: %v", err)
	}

	// Deleting a snapshot removes its file.
	d, _ := e.GetDatabase(bg, ws(e), "app")
	path := e.Layout().SnapshotFile(d.ID, sn2.ID)
	if err := e.DeleteSnapshot(bg, ws(e), "app", sn2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("snapshot file left behind")
	}
}

func TestScheduledSnapshotsAndRetention(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	if _, err := e.SetSchedule(bg, Target{Workspace: ws(e), Database: "app"}, ScheduleOptions{Every: time.Second}); err == nil {
		t.Fatal("sub-minute schedule accepted")
	}
	sc, err := e.SetSchedule(bg, Target{Workspace: ws(e), Database: "app"}, ScheduleOptions{Every: time.Hour, KeepCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if sc.Every != time.Hour || sc.KeepCount != 2 {
		t.Fatalf("schedule: %+v", sc)
	}
	manual, err := e.CreateSnapshot(bg, Target{Workspace: ws(e), Database: "app"}, "keep me")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 4; i++ {
		e.runSchedules(bg, now.Add(time.Duration(i)*time.Hour+time.Second))
	}
	// Not due again within the hour.
	e.runSchedules(bg, now.Add(3*time.Hour+30*time.Minute))
	list, _ := e.ListSnapshots(bg, Target{Workspace: ws(e), Database: "app"})
	var scheduled int
	foundManual := false
	for _, sn := range list {
		if sn.Kind == meta.SnapshotScheduled {
			scheduled++
		}
		if sn.ID == manual.ID {
			foundManual = true
		}
	}
	if scheduled != 2 || !foundManual {
		t.Fatalf("after retention: %d scheduled, manual kept %v", scheduled, foundManual)
	}

	// Age retention.
	if _, err := e.SetSchedule(bg, Target{Workspace: ws(e), Database: "app"}, ScheduleOptions{Every: time.Hour, KeepFor: time.Minute}); err != nil {
		t.Fatal(err)
	}
	e.runSchedules(bg, time.Now().Add(10*time.Hour))
	list, _ = e.ListSnapshots(bg, Target{Workspace: ws(e), Database: "app"})
	scheduled = 0
	for _, sn := range list {
		if sn.Kind == meta.SnapshotScheduled {
			scheduled++
		}
	}
	// The fresh one taken just now is younger than a minute only in real
	// time; retention compares against the tick's clock, so all go.
	if scheduled != 0 {
		t.Fatalf("age retention kept %d", scheduled)
	}
	if err := e.DeleteSchedule(bg, Target{Workspace: ws(e), Database: "app"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.GetSchedule(bg, Target{Workspace: ws(e), Database: "app"}); ok {
		t.Fatal("schedule kept")
	}
}

func TestTrash(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.TrashRetention = time.Hour })
	d := mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	mustQuery(t, e, "app", Write, "INSERT INTO t VALUES (42)")
	if _, err := e.CreateSnapshot(bg, Target{Workspace: ws(e), Database: "app"}, ""); err != nil {
		t.Fatal(err)
	}
	before := e.TotalBytes()
	if before == 0 {
		t.Fatal("no usage recorded")
	}
	del, err := e.DeleteDatabase(bg, ws(e), "app")
	if err != nil {
		t.Fatal(err)
	}
	if del.DeletedAt == nil || del.PurgeAfter == nil {
		t.Fatalf("deleted: %+v", del)
	}
	_, err = query(e, "app", Read, "SELECT 1")
	wantCode(t, err, apierr.DatabaseNotFound, "query a trashed database")
	if e.TotalBytes() != 0 {
		t.Fatalf("trashed bytes still counted: %d", e.TotalBytes())
	}
	if _, err := os.Stat(filepath.Join(e.Layout().TrashEntry(d.ID), "dbs")); err != nil {
		t.Fatal("files not in the trash")
	}
	// The name is free; restoring under it then fails.
	mustCreate(t, e, "app")
	_, err = e.RestoreDatabase(bg, ws(e), d.ID, "")
	wantCode(t, err, apierr.NameTaken, "restore onto a taken name")
	r, err := e.RestoreDatabase(bg, ws(e), d.ID, "app-old")
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "app-old" {
		t.Fatalf("restored: %+v", r)
	}
	if v := scalar(t, mustQuery(t, e, "app-old", Read, "SELECT x FROM t")); v.Int != 42 {
		t.Fatalf("restored data %d", v.Int)
	}
	if snaps, _ := e.ListSnapshots(bg, Target{Workspace: ws(e), Database: "app-old"}); len(snaps) != 1 {
		t.Fatal("snapshot lost in the trash round trip")
	}

	// Purge after retention removes files and records.
	if _, err := e.DeleteDatabase(bg, ws(e), "app-old"); err != nil {
		t.Fatal(err)
	}
	e.purgeTrash(bg, time.Now())
	if _, err := e.RestoreDatabase(bg, ws(e), d.ID, "x"); err != nil {
		t.Fatalf("purged before retention: %v", err)
	}
	if _, err := e.DeleteDatabase(bg, ws(e), d.ID); err != nil {
		t.Fatal(err)
	}
	e.purgeTrash(bg, time.Now().Add(2*time.Hour))
	_, err = e.RestoreDatabase(bg, ws(e), d.ID, "x")
	wantCode(t, err, apierr.DatabaseNotFound, "restore after purge")
	if _, err := os.Stat(e.Layout().TrashEntry(d.ID)); !os.IsNotExist(err) {
		t.Fatal("trash files left after purge")
	}
}
