package backup

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/engine"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// A backup holds every workspace, and a restore brings each back as it
// was: its name, limits, databases, objects and tokens, with the same names
// in two workspaces still two different things. A workspace in the trash
// comes back in the trash, restorable.
func TestBackupAndRestoreWorkspaces(t *testing.T) {
	src := t.TempDir()
	e := open(t, src)
	type side struct {
		ws     meta.Workspace
		db     meta.Database
		bucket meta.Bucket
		token  string
	}
	limit := 7
	mk := func(name string, tokenID string) side {
		t.Helper()
		w, err := e.CreateWorkspace(bg, name, engine.NewWorkspaceLimits{MaxDatabases: &limit})
		if err != nil {
			t.Fatal(err)
		}
		s := side{ws: w, token: tokenID}
		if s.db, err = e.CreateDatabase(bg, w.ID, "notes"); err != nil {
			t.Fatal(err)
		}
		tg := engine.Target{Workspace: w.ID, Database: "notes"}
		for _, sql := range []string{"CREATE TABLE t(v)", "INSERT INTO t VALUES ('row of " + name + "')"} {
			if _, err := e.Query(bg, tg, engine.Write, engine.Statement{SQL: sql}); err != nil {
				t.Fatal(err)
			}
		}
		if s.bucket, err = e.CreateBucket(bg, w.ID, "files"); err != nil {
			t.Fatal(err)
		}
		putObj(t, e, s.bucket.ID, "doc.txt", "object of "+name)
		for i, tok := range []meta.Token{
			{ID: tokenID + "w", Name: "workspace", SecretHash: []byte{1}, WorkspaceID: w.ID, Scope: "workspace", Access: "admin"},
			{ID: tokenID + "g", Name: "grants", SecretHash: []byte{2}, WorkspaceID: w.ID, Scope: "grants", Access: "write",
				Grants: []meta.Grant{{DatabaseID: s.db.ID, Access: "write"}, {BucketID: s.bucket.ID, Access: "read"}}},
		} {
			if _, err := e.Meta().CreateToken(bg, tok, 0); err != nil {
				t.Fatalf("token %d: %v", i, err)
			}
		}
		return s
	}
	acme, globex, old := mk("acme", "tok_aaaaaaaaaaaaaaaaaaa"), mk("globex", "tok_bbbbbbbbbbbbbbbbbbb"), mk("old", "tok_ccccccccccccccccccc")
	if _, err := e.DeleteWorkspace(bg, old.ws.ID); err != nil {
		t.Fatal(err)
	}
	def, err := e.DefaultWorkspace(bg)
	if err != nil {
		t.Fatal(err)
	}

	dir, _, err := Run(bg, Options{DataDir: src, To: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "restored")
	if _, err := Restore(bg, dir, dst); err != nil {
		t.Fatal(err)
	}
	r := open(t, dst)

	check := func(s side) {
		t.Helper()
		w, err := r.GetWorkspace(bg, s.ws.Name)
		if err != nil || w.ID != s.ws.ID || w.Limits.MaxDatabases != limit || w.Default {
			t.Fatalf("%s: %+v %v", s.ws.Name, w, err)
		}
		res, err := r.Query(bg, engine.Target{Workspace: w.ID, Database: "notes"}, engine.Read, engine.Statement{SQL: "SELECT v FROM t"})
		if err != nil || len(res.Rows) != 1 || res.Rows[0][0].Text != "row of "+s.ws.Name {
			t.Fatalf("%s: rows %+v %v", s.ws.Name, res, err)
		}
		b, err := r.GetBucket(bg, w.ID, "files")
		if err != nil || b.ID != s.bucket.ID {
			t.Fatalf("%s: bucket %+v %v", s.ws.Name, b, err)
		}
		_, f, err := r.OpenObject(bg, b.ID, "doc.txt")
		if err != nil {
			t.Fatalf("%s: object: %v", s.ws.Name, err)
		}
		got, _ := io.ReadAll(f)
		f.Close()
		if string(got) != "object of "+s.ws.Name {
			t.Fatalf("%s: object %q", s.ws.Name, got)
		}
		for suffix, scope := range map[string]string{"w": "workspace", "g": "grants"} {
			tok, err := r.Meta().GetToken(bg, s.token+suffix)
			if err != nil || tok.WorkspaceID != w.ID || tok.Scope != scope || (scope == "grants") != (len(tok.Grants) == 2) {
				t.Fatalf("%s: %s token %+v %v", s.ws.Name, scope, tok, err)
			}
		}
		if u, err := r.WorkspaceUsage(bg, w.ID); err != nil || u.Databases != 1 || u.Buckets != 1 || u.Tokens != 2 || u.DatabaseBytes <= 0 || u.ObjectBytes <= 0 {
			t.Fatalf("%s: usage %+v %v", s.ws.Name, u, err)
		}
	}
	check(acme)
	check(globex)
	// The same name in the other workspace is the other workspace's.
	if _, err := r.GetDatabase(bg, acme.ws.ID, globex.db.ID); apierr.As(err).Code != apierr.DatabaseNotFound {
		t.Fatalf("acme finds globex's database after a restore: %v", err)
	}
	if got, err := r.DefaultWorkspace(bg); err != nil || got.ID != def.ID || !got.Default {
		t.Fatalf("default workspace: %+v %v", got, err)
	}
	// The trashed workspace is still in the trash, and still whole.
	if _, err := r.GetWorkspace(bg, "old"); apierr.As(err).Code != apierr.WorkspaceNotFound {
		t.Fatalf("the trashed workspace is live after a restore: %v", err)
	}
	if _, err := r.RestoreWorkspace(bg, old.ws.ID, ""); err != nil {
		t.Fatal(err)
	}
	check(old)
}
