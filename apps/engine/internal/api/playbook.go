package api

// The MCP playbook: the operating rules the tool schemas can't express,
// served by the get_playbook tool so they ship with the engine that enforces
// them. Tool schemas say what an argument is, not which token may call the
// tool, that query can't write, or that a branch never merges back. A copy
// pasted into each client goes stale; this one changes with the deploy.

import (
	_ "embed"
	"fmt"
	"hash/fnv"
	"strings"
)

//go:embed playbook.md
var playbookSource string

// playbookMarkdown is the document get_playbook returns after its version line.
var playbookMarkdown = strings.TrimSpace(playbookSource)

// playbookVersion stamps the playbook with a hash of its text, so it moves
// whenever the rules do and nobody has to bump it by hand. The server
// instructions carry it too, which lets a client that kept the rules from an
// earlier conversation see they are stale without spending a tool call.
var playbookVersion = contentVersion(playbookMarkdown)

// contentVersion is FNV-1a (32-bit) over s, as 8 hex characters: the same
// stamp the other MCP servers with a get_playbook tool use. Changing the
// algorithm makes every cached copy in every client read as stale at once,
// on a day nobody edited the rules.
func contentVersion(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

// mcpInstructions is what initialize returns. It is read in every
// conversation whether or not a tool is called, so it holds what an agent
// needs before its first call (what the server is, that writes are live, and
// where the rules are) and points at get_playbook for the rest.
var mcpInstructions = strings.Join([]string{
	"Lumbung holds SQLite databases, each with branches (full copies), and buckets of files. A token works inside one workspace and sees only what it was given there.",
	"Apps use the same data, so execute, put_object, reset_branch and the delete tools change it for them at once.",
	"",
	"Call get_playbook before your first tool call. It returns the operating rules (playbook version " + playbookVersion + "):",
	"what each token can do, query versus execute, how branches work and the safe way to change a schema, the trash, the result caps, and the error codes.",
	"None of that is inferable from the tool schemas, so do not guess it.",
	"",
	"Once per conversation is enough. If you are carrying rules from an earlier conversation and the version",
	"above does not match the one they came with, they are stale: call get_playbook again and use the new text.",
}, "\n")

// playbookDocument is what get_playbook returns: the version, then the rules.
func playbookDocument() string {
	return "Playbook version: " + playbookVersion + "\n\n" + playbookMarkdown
}
