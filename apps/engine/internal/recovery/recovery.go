// Package recovery keeps a panic in one goroutine from ending the process.
//
// Go ends the whole process when any goroutine panics without recovering.
// net/http recovers the goroutine that serves a request, but not the ones a
// handler or library starts (the MCP SDK runs tool calls on its own
// goroutines), nor timers and background loops. Each of those defers Log.
package recovery

import (
	"log/slog"
	"runtime/debug"
)

// Log recovers a panic and logs it with its stack. Defer it directly, so
// its recover call sees the panic: defer recovery.Log(log, "janitor").
func Log(log *slog.Logger, where string) {
	if v := recover(); v != nil {
		log.Error("recovered panic", "in", where, "panic", v, "stack", string(debug.Stack()))
	}
}
