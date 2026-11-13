package cli_test

// The CLI tested as built binaries against a running lumbungd (PLAN 9 and
// 13: tests against a real engine binary).

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	cliBin   string
	baseURL  string
	adminTok string
)

func TestMain(m *testing.M) {
	code, err := run(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

func run(m *testing.M) (int, error) {
	dir, err := os.MkdirTemp("", "lumbung-cli-test-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	engineDir, _ := filepath.Abs("../..")
	for _, b := range []string{"lumbungd", "lumbung"} {
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, b), "./cmd/"+b)
		cmd.Dir = engineDir
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			return 0, fmt.Errorf("build %s: %w", b, err)
		}
	}
	cliBin = filepath.Join(dir, "lumbung")
	data := filepath.Join(dir, "data")
	eng := exec.Command(filepath.Join(dir, "lumbungd"))
	eng.Env = append(os.Environ(), "LUMBUNG_DATA_DIR="+data, "LUMBUNG_ADDR=127.0.0.1:0", "LUMBUNG_MIN_FREE_DISK=1")
	stderr, _ := eng.StderrPipe()
	if err := eng.Start(); err != nil {
		return 0, err
	}
	defer func() {
		_ = eng.Process.Signal(os.Interrupt)
		_ = eng.Wait()
	}()
	sc := bufio.NewScanner(stderr)
	deadline := time.Now().Add(30 * time.Second)
	for baseURL == "" && time.Now().Before(deadline) && sc.Scan() {
		var line struct {
			Msg  string `json:"msg"`
			Addr string `json:"addr"`
		}
		if json.Unmarshal(sc.Bytes(), &line) == nil && line.Msg == "lumbungd listening" {
			baseURL = "http://" + line.Addr
		}
	}
	if baseURL == "" {
		return 0, fmt.Errorf("lumbungd didn't start")
	}
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	tok, err := os.ReadFile(filepath.Join(data, "secrets", "service-token"))
	if err != nil {
		return 0, err
	}
	adminTok = strings.TrimSpace(string(tok))
	return m.Run(), nil
}

type out struct {
	stdout, stderr string
	err            error
}

// lumbung runs the CLI with its own config file and no inherited LUMBUNG_*
// connection settings.
func lumbung(t *testing.T, cfg, stdin string, args ...string) out {
	t.Helper()
	cmd := exec.Command(cliBin, args...)
	env := []string{"LUMBUNG_CONFIG=" + cfg, "HOME=" + filepath.Dir(cfg)}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "LUMBUNG_") && !strings.HasPrefix(kv, "HOME=") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	return out{so.String(), se.String(), err}
}

func must(t *testing.T, o out) out {
	t.Helper()
	if o.err != nil {
		t.Fatalf("lumbung failed: %v\nstdout: %s\nstderr: %s", o.err, o.stdout, o.stderr)
	}
	return o
}

func fails(t *testing.T, o out, want string) {
	t.Helper()
	if o.err == nil {
		t.Fatalf("lumbung succeeded, want an error with %q\n%s", want, o.stdout)
	}
	if !strings.Contains(o.stderr, want) {
		t.Fatalf("stderr %q doesn't mention %q", o.stderr, want)
	}
}

func loggedIn(t *testing.T, db string) string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "config.json")
	args := []string{"login", "--url", baseURL}
	if db != "" {
		args = append(args, "--db", db)
	}
	must(t, lumbung(t, cfg, adminTok+"\n", args...))
	return cfg
}

func TestLoginAndDatabases(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	fails(t, lumbung(t, cfg, "", "db", "list"), "not logged in")
	fails(t, lumbung(t, cfg, "lmb_aaaaaaaaaaaaaaaaaaaa_"+strings.Repeat("b", 40)+"\n", "login", "--url", baseURL), "unauthorized")
	must(t, lumbung(t, cfg, adminTok+"\n", "login", "--url", baseURL))
	info, err := os.Stat(cfg)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config file mode: %v %v", info, err)
	}

	must(t, lumbung(t, cfg, "", "db", "create", "cli-a"))
	o := must(t, lumbung(t, cfg, "", "db", "list", "--json"))
	var dbs []map[string]any
	if err := json.Unmarshal([]byte(o.stdout), &dbs); err != nil {
		t.Fatalf("db list --json: %v\n%s", err, o.stdout)
	}
	found := false
	for _, d := range dbs {
		found = found || d["name"] == "cli-a"
	}
	if !found {
		t.Fatal("created database not listed")
	}
	if o := must(t, lumbung(t, cfg, "", "db", "list")); !strings.Contains(o.stdout, "cli-a") || !strings.Contains(o.stdout, "NAME") {
		t.Fatalf("table: %s", o.stdout)
	}
	// Destructive commands ask; a wrong answer stops them.
	fails(t, lumbung(t, cfg, "nope\n", "db", "delete", "cli-a"), "not confirmed")
	must(t, lumbung(t, cfg, "cli-a\n", "db", "delete", "cli-a"))
	must(t, lumbung(t, cfg, "", "db", "restore", "cli-a"))
	must(t, lumbung(t, cfg, "", "db", "delete", "cli-a", "--yes"))
	if o := must(t, lumbung(t, cfg, "", "db", "trash")); !strings.Contains(o.stdout, "cli-a") {
		t.Fatalf("trash: %s", o.stdout)
	}
}

func TestQueryAndShell(t *testing.T) {
	cfg := loggedIn(t, "")
	must(t, lumbung(t, cfg, "", "db", "create", "cli-q"))
	cfg = loggedIn(t, "cli-q")
	must(t, lumbung(t, cfg, "", "query", "CREATE TABLE t(id INTEGER PRIMARY KEY, name TEXT, n)"))
	must(t, lumbung(t, cfg, "", "query", "INSERT INTO t (name, n) VALUES (?, ?)", "--arg", "Ana", "--arg", "42"))
	o := must(t, lumbung(t, cfg, "", "query", "SELECT name, n, typeof(n) FROM t", "--json"))
	if !strings.Contains(o.stdout, `"value": "Ana"`) || !strings.Contains(o.stdout, `"value": "42"`) || !strings.Contains(o.stdout, "integer") {
		t.Fatalf("query --json: %s", o.stdout)
	}
	fails(t, lumbung(t, cfg, "", "query", "ATTACH 'x' AS x"), "statement_denied")
	fails(t, lumbung(t, cfg, "", "query", "INSERT INTO t (name) VALUES ('x')", "--read-only"), "read_only")

	shell := must(t, lumbung(t, cfg, ".tables\nSELECT name\n  FROM t;\nSELECT 1 AS a; SELECT 2 AS b;\n.quit\n", "shell"))
	for _, want := range []string{"t\n", "Ana", "a\n1", "b\n2"} {
		if !strings.Contains(shell.stdout, want) {
			t.Fatalf("shell output missing %q:\n%s", want, shell.stdout)
		}
	}
}

func TestMigrate(t *testing.T) {
	cfg := loggedIn(t, "")
	must(t, lumbung(t, cfg, "", "db", "create", "cli-m"))
	cfg = loggedIn(t, "cli-m")
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The shape drizzle-kit generates: breakpoints, and a meta folder to skip.
	write("0000_init.sql", "CREATE TABLE `users` (\n\t`id` integer PRIMARY KEY NOT NULL,\n\t`email` text NOT NULL\n);\n--> statement-breakpoint\nCREATE UNIQUE INDEX `users_email_unique` ON `users` (`email`);")
	write("0001_audit.sql", "CREATE TABLE audit(email TEXT);\n--> statement-breakpoint\nCREATE TRIGGER users_audit AFTER INSERT ON users BEGIN INSERT INTO audit VALUES (new.email); SELECT 1; END;")
	_ = os.MkdirAll(filepath.Join(dir, "meta"), 0o755)
	write("meta/_journal.json", "{}")

	o := must(t, lumbung(t, cfg, "", "migrate", "apply", "--dir", dir))
	if !strings.Contains(o.stdout, "applied 0000_init.sql") || !strings.Contains(o.stdout, "applied 0001_audit.sql") {
		t.Fatalf("apply: %s", o.stdout)
	}
	if o := must(t, lumbung(t, cfg, "", "migrate", "apply", "--dir", dir)); !strings.Contains(o.stdout, "Nothing to apply") {
		t.Fatalf("second apply: %s", o.stdout)
	}
	must(t, lumbung(t, cfg, "", "query", "INSERT INTO users (id, email) VALUES (1, 'a@x')"))
	if o := must(t, lumbung(t, cfg, "", "query", "SELECT count(*) FROM audit")); !strings.Contains(o.stdout, "1") {
		t.Fatalf("trigger from a migration didn't run: %s", o.stdout)
	}

	// A failing migration rolls back whole and isn't recorded.
	write("0002_bad.sql", "CREATE TABLE half(x);\n--> statement-breakpoint\nINSERT INTO nope VALUES (1);")
	fails(t, lumbung(t, cfg, "", "migrate", "apply", "--dir", dir), "0002_bad.sql")
	fails(t, lumbung(t, cfg, "", "query", "SELECT * FROM half"), "no such table")
	st := must(t, lumbung(t, cfg, "", "migrate", "status", "--dir", dir, "--json"))
	var rows []map[string]string
	_ = json.Unmarshal([]byte(st.stdout), &rows)
	if len(rows) != 3 || rows[2]["status"] != "pending" || rows[0]["status"] != "applied" {
		t.Fatalf("status: %s", st.stdout)
	}
	// Editing an applied migration is refused.
	write("0000_init.sql", "CREATE TABLE users(id INTEGER);")
	fails(t, lumbung(t, cfg, "", "migrate", "apply", "--dir", dir), "has changed")
}

func TestBranchesSnapshotsTokensTransfer(t *testing.T) {
	cfg := loggedIn(t, "")
	must(t, lumbung(t, cfg, "", "db", "create", "cli-b"))
	cfg = loggedIn(t, "cli-b")
	must(t, lumbung(t, cfg, "", "query", "CREATE TABLE t(x)"))
	must(t, lumbung(t, cfg, "", "query", "INSERT INTO t VALUES (1)"))

	must(t, lumbung(t, cfg, "", "branch", "create", "dev", "--expires-in-days", "2"))
	must(t, lumbung(t, cfg, "", "query", "ALTER TABLE t ADD COLUMN y", "--branch", "dev"))
	if o := must(t, lumbung(t, cfg, "", "branch", "diff", "dev", "--against", "main")); !strings.Contains(o.stdout, "~ table t") {
		t.Fatalf("diff: %s", o.stdout)
	}
	if o := must(t, lumbung(t, cfg, "", "branch", "list")); !strings.Contains(o.stdout, "expires") || !strings.Contains(o.stdout, "default") {
		t.Fatalf("branch list: %s", o.stdout)
	}
	must(t, lumbung(t, cfg, "dev\n", "branch", "reset", "dev"))

	snap := must(t, lumbung(t, cfg, "", "snapshot", "create", "--label", "before", "--json"))
	var sn map[string]any
	_ = json.Unmarshal([]byte(snap.stdout), &sn)
	id, _ := sn["id"].(string)
	if !strings.HasPrefix(id, "sn_") {
		t.Fatalf("snapshot: %s", snap.stdout)
	}
	must(t, lumbung(t, cfg, "", "snapshot", "restore", id, "--into", "restored"))
	must(t, lumbung(t, cfg, "", "branch", "promote", "restored"))
	must(t, lumbung(t, cfg, "", "branch", "delete", "dev", "--yes"))
	if o := must(t, lumbung(t, cfg, "", "snapshot", "list", "--branch", "main")); !strings.Contains(o.stdout, id) {
		t.Fatalf("snapshot list: %s", o.stdout)
	}

	tok := must(t, lumbung(t, cfg, "", "token", "create", "--name", "ci", "--db", "cli-b", "--access", "read"))
	secret := strings.TrimSpace(tok.stdout)
	if !strings.HasPrefix(secret, "lmb_") || !strings.Contains(tok.stderr, "won't be shown again") {
		t.Fatalf("token create: %q %q", tok.stdout, tok.stderr)
	}
	reader := filepath.Join(t.TempDir(), "config.json")
	must(t, lumbung(t, reader, secret+"\n", "login", "--url", baseURL, "--db", "cli-b"))
	must(t, lumbung(t, reader, "", "query", "SELECT * FROM t"))
	fails(t, lumbung(t, reader, "", "query", "DELETE FROM t"), "read_only")

	file := filepath.Join(t.TempDir(), "copy.db")
	must(t, lumbung(t, cfg, "", "export", "-o", file))
	dump := must(t, lumbung(t, cfg, "", "export", "--format", "sql"))
	if !strings.Contains(dump.stdout, "CREATE TABLE t(x)") {
		t.Fatalf("sql export: %s", dump.stdout)
	}
	must(t, lumbung(t, cfg, "", "db", "create", "cli-b2"))
	must(t, lumbung(t, cfg, "", "import", file, "--db", "cli-b2"))
	if o := must(t, lumbung(t, cfg, "", "query", "SELECT count(*) FROM t", "--db", "cli-b2")); !strings.Contains(o.stdout, "1") {
		t.Fatalf("imported: %s", o.stdout)
	}
	sqlFile := filepath.Join(t.TempDir(), "dump.sql")
	_ = os.WriteFile(sqlFile, []byte(dump.stdout), 0o644)
	must(t, lumbung(t, cfg, "", "db", "create", "cli-b3"))
	must(t, lumbung(t, cfg, "", "import", sqlFile, "--db", "cli-b3"))

	list := must(t, lumbung(t, cfg, "", "token", "list", "--json"))
	var toks []map[string]any
	_ = json.Unmarshal([]byte(list.stdout), &toks)
	var ciID string
	for _, tk := range toks {
		if tk["name"] == "ci" {
			ciID = tk["id"].(string)
		}
	}
	must(t, lumbung(t, cfg, "", "token", "revoke", ciID, "--yes"))
	fails(t, lumbung(t, reader, "", "query", "SELECT 1"), "unauthorized")
}

func TestReviewFixesCLI(t *testing.T) {
	cfg := loggedIn(t, "")
	must(t, lumbung(t, cfg, "", "db", "create", "cli-r"))
	cfg = loggedIn(t, "cli-r")
	must(t, lumbung(t, cfg, "", "query", "CREATE TABLE t(x)"))

	// After promoting a branch, commands without --branch follow the new default.
	must(t, lumbung(t, cfg, "", "branch", "create", "next"))
	must(t, lumbung(t, cfg, "", "query", "INSERT INTO t VALUES (42)", "--branch", "next"))
	must(t, lumbung(t, cfg, "", "branch", "promote", "next"))
	dump := must(t, lumbung(t, cfg, "", "export", "--format", "sql"))
	if !strings.Contains(dump.stdout, "42") {
		t.Fatalf("export used the old default branch: %s", dump.stdout)
	}
	snap := must(t, lumbung(t, cfg, "", "snapshot", "create", "--json"))
	if !strings.Contains(snap.stdout, `"branch_name": "next"`) {
		t.Fatalf("snapshot of the wrong branch: %s", snap.stdout)
	}
	if o := must(t, lumbung(t, cfg, "", "branch", "diff", "main")); !strings.Contains(o.stdout, "No schema changes") {
		t.Fatalf("diff against the default: %s", o.stdout)
	}

	// A --url that isn't the saved one never gets the saved token.
	fails(t, lumbung(t, cfg, "", "db", "list", "--url", "http://127.0.0.1:1"), "pass --token")
	// An unknown profile is an error, not a silent fallback.
	fails(t, lumbung(t, cfg, "", "db", "list", "--profile", "prdo"), "no saved profile")
	// login --profile saves under that name (and makes it current).
	other := loggedIn(t, "")
	must(t, lumbung(t, other, adminTok+"\n", "login", "--url", baseURL, "--profile", "second"))
	must(t, lumbung(t, other, "", "db", "list", "--profile", "second"))
	must(t, lumbung(t, other, "", "db", "list", "--profile", "default"))

	// The shell runs a final statement without a semicolon, and keeps a
	// multi-line trigger together.
	shell := must(t, lumbung(t, cfg, "CREATE TABLE log(x);\nCREATE TRIGGER tr AFTER INSERT ON t BEGIN\n  INSERT INTO log VALUES (new.x);\nEND;\nINSERT INTO t VALUES (7)", "shell"))
	if strings.Contains(shell.stderr, "error") {
		t.Fatalf("shell: %s", shell.stderr)
	}
	if o := must(t, lumbung(t, cfg, "", "query", "SELECT count(*) FROM log")); !strings.Contains(o.stdout, "1") {
		t.Fatalf("trigger or final statement didn't run: %s", o.stdout)
	}

	// --arg: JSON-shaped values must be valid.
	fails(t, lumbung(t, cfg, "", "query", "SELECT ?", "--arg", "99999999999999999999"), "64 bits")
	must(t, lumbung(t, cfg, "", "query", "SELECT ?", "--arg", "hello"))

	// export --json with -o reports the file.
	file := filepath.Join(t.TempDir(), "x.db")
	if o := must(t, lumbung(t, cfg, "", "export", "-o", file, "--json")); !strings.Contains(o.stdout, `"saved"`) {
		t.Fatalf("export --json: %s", o.stdout)
	}
	fails(t, lumbung(t, cfg, "", "export", "--json"), "-o FILE")
}

// A drizzle-kit table rebuild keeps child rows (migration mode), and
// migrate status works read-only on a fresh database.
func TestMigrateTableRebuild(t *testing.T) {
	cfg := loggedIn(t, "")
	must(t, lumbung(t, cfg, "", "db", "create", "cli-fk"))
	cfg = loggedIn(t, "cli-fk")
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "0000_init.sql"), []byte("CREATE TABLE users(id INTEGER PRIMARY KEY, name TEXT);\n--> statement-breakpoint\nCREATE TABLE posts(id INTEGER PRIMARY KEY, author INTEGER REFERENCES users(id) ON DELETE CASCADE);"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "0001_rebuild.sql"), []byte("PRAGMA foreign_keys=OFF;--> statement-breakpoint\nCREATE TABLE `__new_users` (`id` integer PRIMARY KEY, `name` text, `email` text);\n--> statement-breakpoint\nINSERT INTO `__new_users`(\"id\", \"name\") SELECT \"id\", \"name\" FROM `users`;--> statement-breakpoint\nDROP TABLE `users`;--> statement-breakpoint\nALTER TABLE `__new_users` RENAME TO `users`;--> statement-breakpoint\nPRAGMA foreign_keys=ON;"), 0o644)

	reader := filepath.Join(t.TempDir(), "config.json")
	tok := must(t, lumbung(t, cfg, "", "token", "create", "--name", "r", "--db", "cli-fk", "--access", "read"))
	must(t, lumbung(t, reader, strings.TrimSpace(tok.stdout)+"\n", "login", "--url", baseURL, "--db", "cli-fk"))
	if o := must(t, lumbung(t, reader, "", "migrate", "status", "--dir", dir)); !strings.Contains(o.stdout, "pending") {
		t.Fatalf("read-only status: %s", o.stdout)
	}

	// Apply the first file, add data, then the rebuild.
	_ = os.Rename(filepath.Join(dir, "0001_rebuild.sql"), filepath.Join(dir, "later.txt"))
	must(t, lumbung(t, cfg, "", "migrate", "apply", "--dir", dir))
	must(t, lumbung(t, cfg, "", "query", "INSERT INTO users VALUES (1, 'ana')"))
	must(t, lumbung(t, cfg, "", "query", "INSERT INTO posts VALUES (1, 1)"))
	_ = os.Rename(filepath.Join(dir, "later.txt"), filepath.Join(dir, "0001_rebuild.sql"))
	must(t, lumbung(t, cfg, "", "migrate", "apply", "--dir", dir))
	if o := must(t, lumbung(t, cfg, "", "query", "SELECT count(*) FROM posts")); !strings.Contains(o.stdout, "1") {
		t.Fatalf("the rebuild deleted posts: %s", o.stdout)
	}
}
