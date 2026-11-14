package api

import (
	"context"
	"log/slog"

	"github.com/zakiego/lumbung/apps/engine/internal/auth"
)

type ctxKey int

const (
	keyInfo ctxKey = iota
	keyLog
	// keySel holds the workspace a qualified URL names (PLAN 17.3).
	keySel
)

// selFrom returns the workspace a qualified URL named, by name or id, or ""
// for a short URL.
func selFrom(ctx context.Context) string {
	sel, _ := ctx.Value(keySel).(string)
	return sel
}

// requestInfo is per-request state the middleware and handlers share.
type requestInfo struct {
	id        string
	principal auth.Principal
	// Set by handlers and toProblem, read by the audit middleware.
	target string
	code   string
}

func requestInfoFrom(ctx context.Context) *requestInfo {
	info, _ := ctx.Value(keyInfo).(*requestInfo)
	return info
}

// principalFrom returns who the request acts as. Every operation except the
// health check and the OpenAPI document runs behind authentication, so it is
// always set inside a handler.
func principalFrom(ctx context.Context) auth.Principal {
	if info := requestInfoFrom(ctx); info != nil {
		return info.principal
	}
	return auth.Principal{}
}

// setTarget records what a management action touched, for the audit log.
func setTarget(ctx context.Context, target string) {
	if info := requestInfoFrom(ctx); info != nil {
		info.target = target
	}
}

func logFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(keyLog).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
