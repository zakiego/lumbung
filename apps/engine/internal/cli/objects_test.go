package cli_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBucketsAndObjects(t *testing.T) {
	cfg := loggedIn(t, "")
	dir := t.TempDir()
	must(t, lumbung(t, cfg, "", "bucket", "create", "cli-photos"))
	fails(t, lumbung(t, cfg, "", "bucket", "create", "cli-photos"), "name_taken")
	if o := must(t, lumbung(t, cfg, "", "bucket", "list")); !strings.Contains(o.stdout, "cli-photos") {
		t.Fatalf("bucket list: %s", o.stdout)
	}

	file := filepath.Join(dir, "cat.txt")
	if err := os.WriteFile(file, []byte("a very good cat"), 0o600); err != nil {
		t.Fatal(err)
	}
	must(t, lumbung(t, cfg, "", "object", "put", file, "cli-photos/2026/", "--meta", "owner=ana"))
	must(t, lumbung(t, cfg, "", "object", "put", file, "cli-photos/top.txt"))
	o := must(t, lumbung(t, cfg, "", "object", "ls", "cli-photos"))
	if !strings.Contains(o.stdout, "2026/") || !strings.Contains(o.stdout, "top.txt") || strings.Contains(o.stdout, "2026/cat.txt") {
		t.Fatalf("ls: %s", o.stdout)
	}
	o = must(t, lumbung(t, cfg, "", "object", "ls", "cli-photos", "--recursive", "--json"))
	var listing struct {
		Objects []struct {
			Key         string `json:"key"`
			ContentType string `json:"content_type"`
		} `json:"objects"`
	}
	if err := json.Unmarshal([]byte(o.stdout), &listing); err != nil || len(listing.Objects) != 2 || listing.Objects[0].Key != "2026/cat.txt" || !strings.HasPrefix(listing.Objects[0].ContentType, "text/plain") {
		t.Fatalf("ls --recursive --json: %s", o.stdout)
	}
	if o := must(t, lumbung(t, cfg, "", "object", "get", "cli-photos/2026/cat.txt")); o.stdout != "a very good cat" {
		t.Fatalf("get: %q", o.stdout)
	}
	saved := filepath.Join(dir, "saved.txt")
	must(t, lumbung(t, cfg, "", "object", "get", "cli-photos/2026/cat.txt", "-o", saved))
	if b, _ := os.ReadFile(saved); string(b) != "a very good cat" {
		t.Fatalf("get -o: %q", b)
	}
	must(t, lumbung(t, cfg, "", "bucket", "create", "cli-backups"))
	must(t, lumbung(t, cfg, "", "object", "cp", "cli-photos/2026/cat.txt", "cli-backups/"))
	if o := must(t, lumbung(t, cfg, "", "object", "get", "cli-backups/cat.txt")); o.stdout != "a very good cat" {
		t.Fatalf("cp: %q", o.stdout)
	}
	fails(t, lumbung(t, cfg, "", "object", "get", "cli-photos/missing"), "object_not_found")
	fails(t, lumbung(t, cfg, "", "object", "get", "cli-photos"), "BUCKET/KEY")

	// Deleting asks first.
	fails(t, lumbung(t, cfg, "nope\n", "object", "rm", "cli-backups/cat.txt"), "not confirmed")
	must(t, lumbung(t, cfg, "cat.txt\n", "object", "rm", "cli-backups/cat.txt"))
	fails(t, lumbung(t, cfg, "", "object", "get", "cli-backups/cat.txt"), "object_not_found")
	must(t, lumbung(t, cfg, "", "bucket", "delete", "cli-backups", "--yes"))

	// Settings.
	o = must(t, lumbung(t, cfg, "", "bucket", "update", "cli-photos", "--public", "--cors", "https://a.example,https://b.example", "--json"))
	if !strings.Contains(o.stdout, `"public_read": true`) || !strings.Contains(o.stdout, "https://b.example") {
		t.Fatalf("update: %s", o.stdout)
	}
	fails(t, lumbung(t, cfg, "", "bucket", "update", "cli-photos"), "nothing to change")
	res, err := http.Get(baseURL + "/v1/s3/cli-photos/top.txt")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("public read: %v %v", res, err)
	}
	res.Body.Close()

	// A bucket token with an access key, and a presigned URL from it.
	o = must(t, lumbung(t, cfg, "", "token", "create", "--name", "uploader", "--bucket", "cli-photos", "--access", "write", "--access-key", "--json"))
	var tok struct {
		Token           string `json:"token"`
		AccessKeyID     string `json:"access_key_id"`
		SecretAccessKey string `json:"secret_access_key"`
		BucketID        string `json:"bucket_id"`
	}
	if err := json.Unmarshal([]byte(o.stdout), &tok); err != nil || !strings.HasPrefix(tok.AccessKeyID, "LMB") || len(tok.SecretAccessKey) != 40 || tok.BucketID == "" {
		t.Fatalf("token create --access-key: %s", o.stdout)
	}
	bcfg := filepath.Join(t.TempDir(), "config.json")
	must(t, lumbung(t, bcfg, tok.Token+"\n", "login", "--url", baseURL))
	o = must(t, lumbung(t, bcfg, "", "object", "presign", "cli-photos/top.txt", "--expires", "5m"))
	res, err = http.Get(strings.TrimSpace(o.stdout))
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("presigned URL from the CLI: %v %v", res, err)
	}
	res.Body.Close()
	fails(t, lumbung(t, bcfg, "", "bucket", "create", "cli-evil"), "forbidden")
	fails(t, lumbung(t, bcfg, "", "object", "ls", "cli-other"), "bucket_not_found")
	o = must(t, lumbung(t, cfg, "", "token", "list"))
	if !strings.Contains(o.stdout, tok.BucketID) {
		t.Fatalf("token list doesn't show the bucket scope: %s", o.stdout)
	}

	// One token for a database and a bucket, each at its own level.
	must(t, lumbung(t, cfg, "", "db", "create", "cli-agent-db"))
	must(t, lumbung(t, cfg, "", "query", "CREATE TABLE t(x)", "--db", "cli-agent-db"))
	o = must(t, lumbung(t, cfg, "", "token", "create", "--name", "agent", "--grant", "db=cli-agent-db:write", "--grant", "bucket=cli-photos:read", "--json"))
	var agent struct {
		Token  string `json:"token"`
		Scope  string `json:"scope"`
		Grants []struct {
			DatabaseID string `json:"database_id"`
			BucketID   string `json:"bucket_id"`
			Access     string `json:"access"`
		} `json:"grants"`
	}
	if err := json.Unmarshal([]byte(o.stdout), &agent); err != nil || agent.Scope != "grants" || len(agent.Grants) != 2 ||
		agent.Grants[0].Access != "write" || agent.Grants[1].BucketID != tok.BucketID || agent.Grants[1].Access != "read" {
		t.Fatalf("token create --grant: %s", o.stdout)
	}
	acfg := filepath.Join(t.TempDir(), "config.json")
	must(t, lumbung(t, acfg, agent.Token+"\n", "login", "--url", baseURL))
	must(t, lumbung(t, acfg, "", "query", "INSERT INTO t VALUES (1)", "--db", "cli-agent-db"))
	must(t, lumbung(t, acfg, "", "object", "ls", "cli-photos"))
	fails(t, lumbung(t, acfg, "", "object", "rm", "cli-photos/top.txt", "--yes"), "forbidden")
	fails(t, lumbung(t, acfg, "", "object", "ls", "cli-other"), "bucket_not_found")
	o = must(t, lumbung(t, cfg, "", "token", "list"))
	if !strings.Contains(o.stdout, agent.Grants[0].DatabaseID+", "+tok.BucketID) || !strings.Contains(o.stdout, "write, read") {
		t.Fatalf("token list doesn't show both grants: %s", o.stdout)
	}
	fails(t, lumbung(t, cfg, "", "token", "create", "--name", "x", "--grant", "db=cli-agent-db:write", "--bucket", "cli-photos"), "not both")
	for _, bad := range []string{"cli-agent-db", "db=cli-agent-db", "db=:read", "db=cli-agent-db/:read", "table=t:read"} {
		fails(t, lumbung(t, cfg, "", "token", "create", "--name", "x", "--grant", bad), "want db=NAME:ACCESS")
	}
	// Without an access key there's nothing to presign with.
	fails(t, lumbung(t, cfg, "", "object", "presign", "cli-photos/top.txt"), "no_access_key")
}
