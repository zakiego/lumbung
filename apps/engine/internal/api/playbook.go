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

// playbookDocument is what get_playbook returns: the version, then the rules.
func playbookDocument() string {
	return "Playbook version: " + playbookVersion + "\n\n" + playbookMarkdown
}
