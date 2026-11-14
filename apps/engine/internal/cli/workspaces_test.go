package cli_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// firstLine is the token a `token create` prints on standard output.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

func TestWorkspaces(t *testing.T) {
	cfg := loggedIn(t, "") // the instance admin
	created := must(t, lumbung(t, cfg, "", "workspace", "create", "cli-ws", "--max-databases", "2", "--max-object-bytes", "1MiB", "--json"))
	var ws struct {
		ID     string `json:"id"`
		Limits struct {
			MaxDatabases   int   `json:"max_databases"`
			MaxObjectBytes int64 `json:"max_object_bytes"`
		} `json:"limits"`
	}
	if err := json.Unmarshal([]byte(created.stdout), &ws); err != nil || !strings.HasPrefix(ws.ID, "ws_") || ws.Limits.MaxDatabases != 2 || ws.Limits.MaxObjectBytes != 1<<20 {
		t.Fatalf("created: %s", created.stdout)
	}
	fails(t, lumbung(t, cfg, "", "workspace", "create", "cli-ws"), "name_taken")
	fails(t, lumbung(t, cfg, "", "workspace", "create", "x", "--max-object-bytes", "lots"), "max-object-bytes")
	if o := must(t, lumbung(t, cfg, "", "workspace", "list")); !strings.Contains(o.stdout, "cli-ws") || !strings.Contains(o.stdout, "default") || !strings.Contains(o.stdout, "0 of 2") {
		t.Fatalf("list: %s", o.stdout)
	}
	if o := must(t, lumbung(t, cfg, "", "workspace", "show", "cli-ws")); !strings.Contains(o.stdout, "0 of 2") || !strings.Contains(o.stdout, "1.0 MiB") || !strings.Contains(o.stdout, ws.ID) {
		t.Fatalf("show: %s", o.stdout)
	}
	// Without a name, the workspace the token acts in: the default one.
	if o := must(t, lumbung(t, cfg, "", "workspace", "show")); !strings.Contains(o.stdout, "default") || !strings.Contains(o.stdout, "true") {
		t.Fatalf("show current: %s", o.stdout)
	}

	// The workspace's first token, made by the instance admin with --workspace.
	// From then on the workspace's own token needs no flag.
	token := firstLine(must(t, lumbung(t, cfg, "", "token", "create", "--name", "agent", "--access", "admin", "--workspace", "cli-ws")).stdout)
	if !strings.HasPrefix(token, "lmb_") {
		t.Fatalf("token: %q", token)
	}
	agent := filepath.Join(t.TempDir(), "config.json")
	must(t, lumbung(t, agent, token+"\n", "login", "--url", baseURL))
	if o := must(t, lumbung(t, agent, "", "workspace", "show")); !strings.Contains(o.stdout, "cli-ws") || !strings.Contains(o.stdout, "false") {
		t.Fatalf("the agent's workspace: %s", o.stdout)
	}
	if o := must(t, lumbung(t, agent, "", "workspace", "list")); strings.Contains(o.stdout, "default") || !strings.Contains(o.stdout, "cli-ws") {
		t.Fatalf("the agent lists workspaces: %s", o.stdout)
	}
	must(t, lumbung(t, agent, "", "db", "create", "notes"))
	must(t, lumbung(t, agent, "", "query", "CREATE TABLE t(v)", "--db", "notes"))
	must(t, lumbung(t, agent, "", "query", "INSERT INTO t VALUES ('kept')", "--db", "notes"))
	must(t, lumbung(t, agent, "", "bucket", "create", "files"))
	must(t, lumbung(t, agent, "", "db", "create", "second"))
	fails(t, lumbung(t, agent, "", "db", "create", "third"), "limit_reached")
	if o := must(t, lumbung(t, agent, "", "db", "list")); !strings.Contains(o.stdout, "notes") || strings.Contains(o.stdout, "cli-b") {
		t.Fatalf("the agent's databases: %s", o.stdout)
	}
	// What belongs to the instance is closed to it, and another workspace
	// looks like one that doesn't exist.
	fails(t, lumbung(t, agent, "", "workspace", "create", "mine"), "forbidden")
	fails(t, lumbung(t, agent, "", "workspace", "update", "cli-ws", "--max-databases", "0"), "forbidden")
	fails(t, lumbung(t, agent, "", "workspace", "show", "default"), "workspace_not_found")
	fails(t, lumbung(t, agent, "", "db", "list", "--workspace", "default"), "workspace_not_found")
	fails(t, lumbung(t, agent, "", "db", "list", "-w", "nope"), "workspace_not_found")
	fails(t, lumbung(t, agent, "", "token", "list", "--instance"), "forbidden")
	// A token it makes without a target is a workspace token.
	if o := must(t, lumbung(t, agent, "", "token", "create", "--name", "reader", "--json")); !strings.Contains(o.stdout, `"scope": "workspace"`) {
		t.Fatalf("a token without a target: %s", o.stdout)
	}

	// The instance admin reaches the workspace with the flag, the
	// environment variable, or a profile that saved it.
	if o := must(t, lumbung(t, cfg, "", "db", "list", "-w", "cli-ws")); !strings.Contains(o.stdout, "notes") {
		t.Fatalf("db list -w: %s", o.stdout)
	}
	if o := must(t, lumbung(t, cfg, "", "db", "list")); strings.Contains(o.stdout, "notes") {
		t.Fatalf("db list in the default workspace shows the other workspace's: %s", o.stdout)
	}
	if o := must(t, lumbung(t, cfg, "", "query", "SELECT v FROM t", "--db", "notes", "--workspace", ws.ID)); !strings.Contains(o.stdout, "kept") {
		t.Fatalf("query by workspace id: %s", o.stdout)
	}
	saved := filepath.Join(t.TempDir(), "config.json")
	must(t, lumbung(t, saved, adminTok+"\n", "login", "--url", baseURL, "--workspace", "cli-ws", "--db", "notes"))
	if o := must(t, lumbung(t, saved, "", "query", "SELECT v FROM t")); !strings.Contains(o.stdout, "kept") {
		t.Fatalf("a profile with a workspace: %s", o.stdout)
	}
	// The flag wins over the profile.
	fails(t, lumbung(t, saved, "", "query", "SELECT v FROM t", "-w", "default"), "database_not_found")

	// Change it.
	if o := must(t, lumbung(t, cfg, "", "workspace", "update", "cli-ws", "--name", "cli-ws2", "--max-databases", "5")); !strings.Contains(o.stdout, "cli-ws2") || !strings.Contains(o.stdout, "2 of 5") {
		t.Fatalf("update: %s", o.stdout)
	}
	fails(t, lumbung(t, cfg, "", "workspace", "update", "cli-ws2"), "nothing to change")
	must(t, lumbung(t, agent, "", "db", "create", "third"))

	// Move a database and a bucket out of the default workspace.
	must(t, lumbung(t, cfg, "", "db", "create", "cli-moving"))
	must(t, lumbung(t, cfg, "", "bucket", "create", "cli-moving-files"))
	fails(t, lumbung(t, agent, "", "db", "move", "notes", "--to", "default", "--yes"), "forbidden")
	fails(t, lumbung(t, cfg, "", "db", "move", "cli-moving", "--to", "nope", "--yes"), "workspace_not_found")
	if o := must(t, lumbung(t, cfg, "cli-moving\n", "db", "move", "cli-moving", "--to", "cli-ws2")); !strings.Contains(o.stdout, "Moved database cli-moving") {
		t.Fatalf("db move: %s", o.stdout)
	}
	must(t, lumbung(t, cfg, "", "bucket", "move", "cli-moving-files", "--to", ws.ID, "--yes"))
	if o := must(t, lumbung(t, agent, "", "db", "list")); !strings.Contains(o.stdout, "cli-moving") {
		t.Fatalf("the moved database isn't in the workspace: %s", o.stdout)
	}
	if o := must(t, lumbung(t, agent, "", "bucket", "list")); !strings.Contains(o.stdout, "cli-moving-files") {
		t.Fatalf("the moved bucket isn't in the workspace: %s", o.stdout)
	}

	// Instance tokens.
	op := must(t, lumbung(t, cfg, "", "token", "create", "--instance", "--name", "cli-operator", "--access", "read", "--json"))
	var made struct {
		ID    string `json:"id"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal([]byte(op.stdout), &made); err != nil || made.Scope != "instance" {
		t.Fatalf("instance token: %s", op.stdout)
	}
	fails(t, lumbung(t, cfg, "", "token", "create", "--instance", "--name", "x", "--db", "cli-moving"), "belongs to no workspace")
	if o := must(t, lumbung(t, cfg, "", "token", "list", "--instance")); !strings.Contains(o.stdout, "cli-operator") {
		t.Fatalf("instance tokens: %s", o.stdout)
	}
	if o := must(t, lumbung(t, cfg, "", "token", "list")); strings.Contains(o.stdout, "cli-operator") {
		t.Fatalf("the default workspace's tokens hold an instance token: %s", o.stdout)
	}
	fails(t, lumbung(t, cfg, "", "token", "revoke", made.ID, "--yes"), "token_not_found")
	must(t, lumbung(t, cfg, "", "token", "revoke", made.ID, "--instance", "--yes"))

	// Delete and restore.
	fails(t, lumbung(t, cfg, "wrong\n", "workspace", "delete", "cli-ws2"), "not confirmed")
	fails(t, lumbung(t, cfg, "", "workspace", "delete", "default", "--yes"), "forbidden")
	if o := must(t, lumbung(t, cfg, "cli-ws2\n", "workspace", "delete", "cli-ws2")); !strings.Contains(o.stdout, "workspace restore") {
		t.Fatalf("delete: %s", o.stdout)
	}
	fails(t, lumbung(t, agent, "", "db", "list"), "unauthorized")
	if o := must(t, lumbung(t, cfg, "", "workspace", "trash")); !strings.Contains(o.stdout, "cli-ws2") {
		t.Fatalf("trash: %s", o.stdout)
	}
	if o := must(t, lumbung(t, cfg, "", "workspace", "list")); strings.Contains(o.stdout, "cli-ws2") {
		t.Fatalf("list after delete: %s", o.stdout)
	}
	must(t, lumbung(t, cfg, "", "workspace", "restore", ws.ID, "--name", "cli-ws3"))
	if o := must(t, lumbung(t, agent, "", "query", "SELECT v FROM t", "--db", "notes")); !strings.Contains(o.stdout, "kept") {
		t.Fatalf("after the restore: %s", o.stdout)
	}
	if o := must(t, lumbung(t, agent, "", "workspace", "show")); !strings.Contains(o.stdout, "cli-ws3") {
		t.Fatalf("restored under a new name: %s", o.stdout)
	}
}
