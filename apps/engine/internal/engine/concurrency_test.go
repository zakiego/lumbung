package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/config"
)

// PLAN 6.5: a copy taken while writes are running passes integrity_check and
// holds only committed transactions. Each write transaction inserts a pair
// of rows with the same batch number, so a copy with a half transaction
// would show a batch with one row.
func TestCopyDuringWrites(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.MaxBranches = 200 })
	d := mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE pairs(batch INTEGER, side INTEGER, pad BLOB)")

	ctx, cancel := context.WithCancel(bg)
	var wg sync.WaitGroup
	var batches atomic.Int64
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				n := batches.Add(1)
				_, err := e.Batch(bg, Target{Database: "app"}, Write, []Statement{
					stmt("INSERT INTO pairs VALUES (?, 1, randomblob(2000))", n),
					stmt("INSERT INTO pairs VALUES (?, 2, randomblob(2000))", n),
				})
				if err != nil {
					t.Errorf("writer: %v", err)
					return
				}
			}
		}()
	}

	check := func(branch string) {
		r := mustOnBranch(t, e, "app", branch, Read, "SELECT count(*) FROM (SELECT batch FROM pairs GROUP BY batch HAVING count(*) != 2)")
		if v := scalar(t, r); v.Int != 0 {
			t.Errorf("%s holds %d half transactions", branch, v.Int)
		}
	}
	for i := 0; i < 15; i++ {
		time.Sleep(10 * time.Millisecond)
		name := fmt.Sprintf("copy-%d", i)
		b, err := e.CreateBranch(bg, "app", name, BranchSource{}, NewBranchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := checkIntegrity(bg, e.Layout().BranchFile(d.ID, b.ID)); err != nil {
			t.Fatal(err)
		}
		check(name)
		sn, err := e.CreateSnapshot(bg, Target{Database: "app"}, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := checkIntegrity(bg, e.Layout().SnapshotFile(d.ID, sn.ID)); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	wg.Wait()
	check("main")
	if batches.Load() < 10 {
		t.Fatalf("only %d write batches ran; the copies blocked writers", batches.Load())
	}
}

// Requests in flight during a reset or delete either finish before it or
// fail cleanly after; none sees a half-swapped file.
func TestMaintenanceDuringRequests(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.MaxBranches = 200 })
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	mustQuery(t, e, "app", Write, "INSERT INTO t VALUES (1)")
	if _, err := e.CreateBranch(bg, "app", "dev", BranchSource{}, NewBranchOptions{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				r, err := onBranch(e, "app", "dev", Read, "SELECT count(*) FROM t")
				if err != nil {
					t.Errorf("read during reset: %v", err)
					return
				}
				if v := r.Rows[0][0].Int; v < 1 {
					t.Errorf("read saw %d rows", v)
					return
				}
				if _, err := onBranch(e, "app", "dev", Write, "INSERT INTO t VALUES (2)"); err != nil {
					t.Errorf("write during reset: %v", err)
					return
				}
			}
		}()
	}
	for i := 0; i < 10; i++ {
		if _, err := e.ResetBranch(bg, "app", "dev"); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	wg.Wait()
}

// PLAN 13 crash test: kill the process mid-write, reopen, integrity_check
// passes and every committed transaction is there. The child is this test
// binary re-run with LUMBUNG_CRASH_DIR set.
func TestCrashMidWrite(t *testing.T) {
	if dir := os.Getenv("LUMBUNG_CRASH_DIR"); dir != "" {
		crashChild(dir)
		return
	}
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.DataDir = dir
	e, err := Open(bg, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE log(n INTEGER PRIMARY KEY, pad BLOB)")
	mustQuery(t, e, "app", Write, "CREATE TABLE counter(n INTEGER)")
	mustQuery(t, e, "app", Write, "INSERT INTO counter VALUES (0)")
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	for round := 0; round < 3; round++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCrashMidWrite$")
		cmd.Env = append(os.Environ(), "LUMBUNG_CRASH_DIR="+dir)
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		// Wait until the child reports some commits, then kill it hard.
		buf := make([]byte, 4096)
		var seen strings.Builder
		deadline := time.Now().Add(20 * time.Second)
		for !strings.Contains(seen.String(), "committed 20") && time.Now().Before(deadline) {
			n, err := out.Read(buf)
			seen.Write(buf[:n])
			if err != nil {
				break
			}
		}
		_ = cmd.Process.Signal(syscall.SIGKILL)
		_ = cmd.Wait()
		lines := strings.Fields(seen.String())
		last := int64(0)
		for i := 0; i+1 < len(lines); i++ {
			if lines[i] == "committed" {
				if n, err := strconv.ParseInt(lines[i+1], 10, 64); err == nil {
					last = n
				}
			}
		}
		if last < 20 {
			t.Fatalf("round %d: child committed only %d before the kill\n%s", round, last, seen.String())
		}

		e, err := Open(bg, cfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		d, _ := e.GetDatabase(bg, "app")
		if err := checkIntegrity(bg, e.Layout().BranchFile(d.ID, d.DefaultBranchID)); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		cnt := scalar(t, mustQuery(t, e, "app", Read, "SELECT count(*) FROM log")).Int
		ctr := scalar(t, mustQuery(t, e, "app", Read, "SELECT n FROM counter")).Int
		if cnt != ctr {
			t.Fatalf("round %d: %d log rows but counter %d: a transaction was split", round, cnt, ctr)
		}
		if cnt < last {
			t.Fatalf("round %d: %d rows, but %d were reported committed", round, cnt, last)
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func crashChild(dir string) {
	cfg := config.Defaults()
	cfg.DataDir = dir
	e, err := Open(bg, cfg, nil)
	if err != nil {
		fmt.Println("open failed:", err)
		os.Exit(1)
	}
	for {
		res, err := e.Batch(bg, Target{Database: "app"}, Write, []Statement{
			stmt("INSERT INTO log (pad) VALUES (randomblob(4000))"),
			stmt("UPDATE counter SET n = n + 1"),
			stmt("SELECT n FROM counter"),
		})
		if err != nil {
			fmt.Println("write failed:", err)
			os.Exit(1)
		}
		fmt.Printf("committed %d\n", res[2].Rows[0][0].Int)
	}
}
