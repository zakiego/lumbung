package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zakiego/lumbung/apps/engine/internal/cli"
)

// cliReference renders the CLI reference from the command tree, so the page
// says exactly what `lumbung <command> --help` says.
func cliReference() []byte {
	root := cli.Root(cli.Options{Out: io.Discard, Err: io.Discard, Getenv: func(string) string { return "" }, ConfigPath: "unused"})
	var b bytes.Buffer
	b.WriteString(`---
title: CLI
description: Every lumbung command and flag, generated from the CLI's own help.
sidebar:
  order: 3
---

`)
	b.WriteString(banner)
	b.WriteString(root.Long + "\n\n")
	b.WriteString("Every command takes these flags:\n\n```text\n" + strings.TrimRight(root.PersistentFlags().FlagUsages(), "\n") + "\n```\n\n")
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			if sub.HasSubCommands() {
				fmt.Fprintf(&b, "## %s\n\n%s.\n\n", sub.CommandPath(), sub.Short)
				walk(sub)
				continue
			}
			fmt.Fprintf(&b, "### %s\n\n%s.\n\n", sub.CommandPath(), sub.Short)
			if sub.Long != "" {
				b.WriteString(sub.Long + "\n\n")
			}
			fmt.Fprintf(&b, "```text\n%s\n```\n\n", sub.UseLine())
			if flags := strings.TrimRight(sub.LocalNonPersistentFlags().FlagUsages(), "\n"); flags != "" {
				b.WriteString("```text\n" + flags + "\n```\n\n")
			}
			if sub.Example != "" {
				b.WriteString("```sh\n" + strings.TrimRight(dedent(sub.Example), "\n") + "\n```\n\n")
			}
		}
	}
	walk(root)
	return b.Bytes()
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, "  ")
	}
	return strings.Join(lines, "\n")
}
