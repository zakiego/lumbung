package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// restAPI renders the REST API reference from the committed openapi.json.
func restAPI(specPath string) ([]byte, error) {
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return nil, err
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, err
	}
	schemas, _ := dig(spec, "components", "schemas").(map[string]any)
	resolve := func(s map[string]any) map[string]any {
		for i := 0; i < 10; i++ {
			ref, ok := s["$ref"].(string)
			if !ok {
				return s
			}
			name := strings.TrimPrefix(ref, "#/components/schemas/")
			next, ok := schemas[name].(map[string]any)
			if !ok {
				return s
			}
			s = next
		}
		return s
	}

	type opInfo struct {
		method, path string
		op           map[string]any
	}
	byTag := map[string][]opInfo{}
	var tags []string
	paths, _ := spec["paths"].(map[string]any)
	for path, item := range paths {
		for method, raw := range item.(map[string]any) {
			op, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			tag := "Other"
			if ts, ok := op["tags"].([]any); ok && len(ts) > 0 {
				tag = ts[0].(string)
			}
			if _, seen := byTag[tag]; !seen {
				tags = append(tags, tag)
			}
			byTag[tag] = append(byTag[tag], opInfo{strings.ToUpper(method), path, op})
		}
	}
	order := map[string]int{"Databases": 0, "Branches": 1, "Query": 2, "Snapshots": 3, "Import and download": 4, "Tokens": 5, "Audit": 6}
	sort.Slice(tags, func(i, j int) bool { return order[tags[i]] < order[tags[j]] })

	var b bytes.Buffer
	b.WriteString(`---
title: REST API
description: Every endpoint of the Lumbung REST API, generated from its OpenAPI document.
sidebar:
  order: 1
---

`)
	b.WriteString(banner)
	b.WriteString("Every endpoint is under `/v1` and needs `Authorization: Bearer <token>`. `{db}` and `{branch}` take a name or an id. The machine-readable document is served at `/v1/openapi.json` and committed as `apps/engine/openapi.json`.\n\n")
	b.WriteString("- Errors are `application/problem+json` with a stable `code`; see [Error codes](/docs/reference/error-codes/).\n")
	b.WriteString("- Values in rows and parameters use the typed encoding in [Value encoding](/docs/reference/value-encoding/).\n")
	b.WriteString("- Lists take `limit` (1 to 200, default 50) and `cursor`, and return `data` and `next_cursor` (empty on the last page).\n")
	b.WriteString("- Create endpoints accept an `Idempotency-Key` header: a retry with the same key returns the first response.\n")
	b.WriteString("- Every response carries `X-Request-Id`.\n\n")

	for _, tag := range tags {
		ops := byTag[tag]
		sort.Slice(ops, func(i, j int) bool {
			if ops[i].path != ops[j].path {
				return ops[i].path < ops[j].path
			}
			return methodRank(ops[i].method) < methodRank(ops[j].method)
		})
		fmt.Fprintf(&b, "## %s\n\n", tag)
		for _, o := range ops {
			summary, _ := o.op["summary"].(string)
			fmt.Fprintf(&b, "### %s\n\n`%s %s`\n\n", summary, o.method, o.path)
			if d, _ := o.op["description"].(string); d != "" {
				b.WriteString(d + "\n\n")
			}
			if params, ok := o.op["parameters"].([]any); ok && len(params) > 0 {
				b.WriteString("| Parameter | In | Type | Description |\n|---|---|---|---|\n")
				for _, raw := range params {
					p := raw.(map[string]any)
					sch, _ := p["schema"].(map[string]any)
					req := ""
					if r, _ := p["required"].(bool); r {
						req = " (required)"
					}
					desc, _ := p["description"].(string)
					if desc == "" && sch != nil {
						desc, _ = sch["description"].(string)
					}
					fmt.Fprintf(&b, "| `%s`%s | %s | %s | %s |\n", p["name"], req, p["in"], typeOf(sch), cell(desc))
				}
				b.WriteString("\n")
			}
			if body, ok := dig(o.op, "requestBody", "content").(map[string]any); ok {
				if js, ok := dig(body, "application/json", "schema").(map[string]any); ok {
					fields(&b, "Request body", resolve(js), resolve)
				} else {
					var cts []string
					for ct := range body {
						cts = append(cts, "`"+ct+"`")
					}
					sort.Strings(cts)
					fmt.Fprintf(&b, "Request body: the raw file, as %s.\n\n", strings.Join(cts, " or "))
				}
			}
			if resps, ok := o.op["responses"].(map[string]any); ok {
				for code, raw := range resps {
					if !strings.HasPrefix(code, "2") {
						continue
					}
					r := raw.(map[string]any)
					if js, ok := dig(r, "content", "application/json", "schema").(map[string]any); ok {
						fields(&b, "Response `"+code+"`", resolve(js), resolve)
					} else if code == "204" {
						b.WriteString("Response `204`: no content.\n\n")
					} else if c, ok := r["content"].(map[string]any); ok {
						var cts []string
						for ct := range c {
							cts = append(cts, "`"+ct+"`")
						}
						sort.Strings(cts)
						fmt.Fprintf(&b, "Response `%s`: the file, as %s.\n\n", code, strings.Join(cts, " or "))
					}
				}
			}
		}
	}
	return b.Bytes(), nil
}

func methodRank(m string) int {
	return map[string]int{"GET": 0, "POST": 1, "PUT": 2, "PATCH": 3, "DELETE": 4}[m]
}

func dig(m any, keys ...string) any {
	for _, k := range keys {
		mm, ok := m.(map[string]any)
		if !ok {
			return nil
		}
		m = mm[k]
	}
	return m
}

func typeOf(s map[string]any) string {
	if s == nil {
		return ""
	}
	if ref, ok := s["$ref"].(string); ok {
		return strings.TrimPrefix(ref, "#/components/schemas/")
	}
	if t, ok := s["type"].(string); ok {
		if t == "array" {
			if items, ok := s["items"].(map[string]any); ok {
				return typeOf(items) + "[]"
			}
		}
		if e, ok := s["enum"].([]any); ok {
			parts := make([]string, len(e))
			for i, v := range e {
				parts[i] = fmt.Sprint(v)
			}
			return t + ": " + strings.Join(parts, ", ")
		}
		return t
	}
	if t, ok := s["type"].([]any); ok {
		parts := make([]string, len(t))
		for i, v := range t {
			parts[i] = fmt.Sprint(v)
		}
		return strings.Join(parts, " or ")
	}
	if _, ok := s["oneOf"]; ok {
		return "value"
	}
	return "any"
}

func fields(b *bytes.Buffer, title string, s map[string]any, resolve func(map[string]any) map[string]any) {
	props, ok := s["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		fmt.Fprintf(b, "%s: `%s`.\n\n", title, typeOf(s))
		return
	}
	required := map[string]bool{}
	if r, ok := s["required"].([]any); ok {
		for _, v := range r {
			required[v.(string)] = true
		}
	}
	names := make([]string, 0, len(props))
	for n := range props {
		if n != "$schema" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	fmt.Fprintf(b, "%s:\n\n| Field | Type | Description |\n|---|---|---|\n", title)
	for _, n := range names {
		p := props[n].(map[string]any)
		desc, _ := p["description"].(string)
		if desc == "" {
			desc, _ = resolve(p)["description"].(string)
		}
		req := ""
		if required[n] {
			req = " (required)"
		}
		fmt.Fprintf(b, "| `%s`%s | %s | %s |\n", n, req, cell(typeOf(p)), cell(desc))
	}
	b.WriteString("\n")
}
