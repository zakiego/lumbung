package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/lumbung/apps/engine/internal/api"
	"github.com/zakiego/lumbung/apps/engine/internal/server"
)

// mcpReference lists the MCP tools as a client sees them: it connects to the
// server over an in-memory transport and asks for tools/list.
func mcpReference() ([]byte, error) {
	var tools []*mcp.Tool
	err := server.WithThrowawayAPI(func(a *api.Server) error {
		ctx := context.Background()
		ct, st := mcp.NewInMemoryTransports()
		if _, err := a.MCPServer().Connect(ctx, st, nil); err != nil {
			return err
		}
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "docgen", Version: "1"}, nil).Connect(ctx, ct, nil)
		if err != nil {
			return err
		}
		defer cs.Close()
		res, err := cs.ListTools(ctx, nil)
		if err != nil {
			return err
		}
		tools = res.Tools
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })

	var b bytes.Buffer
	b.WriteString(`---
title: MCP tools
description: Every tool the /mcp endpoint offers, with its inputs and hints, generated from the server.
sidebar:
  order: 4
---

`)
	b.WriteString(banner)
	b.WriteString("The engine serves MCP over streamable HTTP at `/mcp`, with the same bearer tokens and scopes as the REST API. See [Use from an AI agent](/docs/guides/ai-agents/) to connect a client.\n\n")
	b.WriteString("| Tool | Read-only | Destructive | What it does |\n|---|---|---|---|\n")
	for _, t := range tools {
		ro, de := "yes", "no"
		if a := t.Annotations; a != nil {
			if !a.ReadOnlyHint {
				ro = "no"
				if a.DestructiveHint == nil || *a.DestructiveHint {
					de = "yes"
				}
			}
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", t.Name, ro, de, cell(t.Description))
	}
	b.WriteString("\n")
	for _, t := range tools {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", t.Name, t.Description)
		raw, _ := json.Marshal(t.InputSchema)
		var schema struct {
			Properties map[string]struct {
				Type        any    `json:"type"`
				Description string `json:"description"`
			} `json:"properties"`
			Required []string `json:"required"`
		}
		_ = json.Unmarshal(raw, &schema)
		if len(schema.Properties) == 0 {
			b.WriteString("No inputs.\n\n")
			continue
		}
		required := map[string]bool{}
		for _, r := range schema.Required {
			required[r] = true
		}
		names := make([]string, 0, len(schema.Properties))
		for n := range schema.Properties {
			names = append(names, n)
		}
		sort.Strings(names)
		b.WriteString("| Input | Type | Description |\n|---|---|---|\n")
		for _, n := range names {
			p := schema.Properties[n]
			req := ""
			if required[n] {
				req = " (required)"
			}
			typ := fmt.Sprint(p.Type)
			if list, ok := p.Type.([]any); ok {
				parts := make([]string, len(list))
				for i, v := range list {
					parts[i] = fmt.Sprint(v)
				}
				typ = strings.Join(parts, " or ")
			}
			fmt.Fprintf(&b, "| `%s`%s | %s | %s |\n", n, req, typ, cell(p.Description))
		}
		b.WriteString("\n")
	}
	return b.Bytes(), nil
}
