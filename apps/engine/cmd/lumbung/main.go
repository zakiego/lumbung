// Command lumbung is the Lumbung CLI.
package main

import (
	"fmt"
	"os"

	"github.com/zakiego/lumbung/apps/engine/internal/version"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println(version.Version)
		return
	}
	fmt.Fprintln(os.Stderr, "usage: lumbung version")
	os.Exit(2)
}
