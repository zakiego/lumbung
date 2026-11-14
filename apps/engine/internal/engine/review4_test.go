package engine

import (
	"testing"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
)

// A drizzle-kit style table rebuild in migration mode keeps child rows that
// ON DELETE CASCADE would otherwise remove; a migration that leaves a broken
// reference is refused whole.
func TestMigrationBatchKeepsChildRows(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "a")
	for _, sql := range []string{
		"CREATE TABLE users(id INTEGER PRIMARY KEY, name TEXT)",
		"CREATE TABLE posts(id INTEGER PRIMARY KEY, author INTEGER REFERENCES users(id) ON DELETE CASCADE)",
		"INSERT INTO users VALUES (1, 'ana')",
		"INSERT INTO posts VALUES (1, 1)",
	} {
		mustQuery(t, e, "a", Write, sql)
	}
	rebuild := []Statement{
		stmt("PRAGMA foreign_keys=OFF"),
		stmt("CREATE TABLE __new_users(id INTEGER PRIMARY KEY, name TEXT, email TEXT)"),
		stmt("INSERT INTO __new_users (id, name) SELECT id, name FROM users"),
		stmt("DROP TABLE users"),
		stmt("ALTER TABLE __new_users RENAME TO users"),
		stmt("PRAGMA foreign_keys=ON"),
	}
	if _, err := e.BatchWith(bg, Target{Workspace: ws(e), Database: "a"}, Write, rebuild, BatchOptions{Migration: true}); err != nil {
		t.Fatal(err)
	}
	if v := scalar(t, mustQuery(t, e, "a", Read, "SELECT count(*) FROM posts")); v.Int != 1 {
		t.Fatalf("the rebuild deleted child rows: %d left", v.Int)
	}
	// Without migration mode the same pattern cascades. That's why the mode exists.
	mustQuery(t, e, "a", Write, "CREATE TABLE tags(id INTEGER PRIMARY KEY)")
	mustQuery(t, e, "a", Write, "CREATE TABLE post_tags(tag INTEGER REFERENCES tags(id) ON DELETE CASCADE)")
	mustQuery(t, e, "a", Write, "INSERT INTO tags VALUES (1)")
	mustQuery(t, e, "a", Write, "INSERT INTO post_tags VALUES (1)")
	if _, err := e.Batch(bg, Target{Workspace: ws(e), Database: "a"}, Write, []Statement{stmt("DROP TABLE tags"), stmt("CREATE TABLE tags(id INTEGER PRIMARY KEY)"), stmt("INSERT INTO tags VALUES (1)")}); err != nil {
		t.Fatal(err)
	}
	if v := scalar(t, mustQuery(t, e, "a", Read, "SELECT count(*) FROM post_tags")); v.Int != 0 {
		t.Fatalf("expected the plain batch to cascade, %d rows left", v.Int)
	}

	_, err := e.BatchWith(bg, Target{Workspace: ws(e), Database: "a"}, Write, []Statement{stmt("INSERT INTO posts VALUES (2, 99)")}, BatchOptions{Migration: true})
	wantCode(t, err, apierr.ConstraintViolation, "dangling reference in a migration")
	if v := scalar(t, mustQuery(t, e, "a", Read, "SELECT count(*) FROM posts")); v.Int != 1 {
		t.Fatal("a refused migration was kept")
	}
	// Foreign keys are back on for the next request.
	_, err = query(e, "a", Write, "INSERT INTO posts VALUES (3, 99)")
	wantCode(t, err, apierr.ConstraintViolation, "foreign keys after a migration")
	_, err = e.BatchWith(bg, Target{Workspace: ws(e), Database: "a"}, Read, []Statement{stmt("SELECT 1")}, BatchOptions{Migration: true})
	wantCode(t, err, apierr.ReadOnly, "migration with read access")
}
