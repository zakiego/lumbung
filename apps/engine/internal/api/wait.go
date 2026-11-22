package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/engine"
)

// maxWaitSeconds caps how long one wait is held. It stays well under the
// 100 second idle limit common to proxies and tunnels.
const maxWaitSeconds = 60

// maxWaitsPerToken caps the waits one token holds open at once.
const maxWaitsPerToken = 16

type waitIn struct {
	DB      string `path:"db"`
	Branch  string `path:"branch"`
	Cursor  string `query:"cursor" maxLength:"200" doc:"The cursor from the last answer. Leave it out to get the current cursor at once."`
	Timeout int    `query:"timeout" minimum:"0" maximum:"60" default:"30" doc:"Seconds to wait for a change before answering with changed false."`
}

type defaultWaitIn struct {
	DB      string `path:"db"`
	Cursor  string `query:"cursor" maxLength:"200" doc:"The cursor from the last answer. Leave it out to get the current cursor at once."`
	Timeout int    `query:"timeout" minimum:"0" maximum:"60" default:"30" doc:"Seconds to wait for a change before answering with changed false."`
}

type waitOut struct {
	Body struct {
		Cursor  string `json:"cursor" doc:"Names the branch's state now. Pass it to the next wait."`
		Changed bool   `json:"changed" doc:"True when the branch changed since the cursor you passed. It can be a false alarm, never a miss."`
	}
}

// waiters counts the waits each token holds open.
type waiters struct {
	mu sync.Mutex
	n  map[string]int
}

func (w *waiters) take(tokenID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.n == nil {
		w.n = map[string]int{}
	}
	if w.n[tokenID] >= maxWaitsPerToken {
		return false
	}
	w.n[tokenID]++
	return true
}

func (w *waiters) give(tokenID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.n[tokenID]--; w.n[tokenID] <= 0 {
		delete(w.n, tokenID)
	}
}

// Drain answers every wait in flight with changed false and makes new ones
// answer at once, so a graceful shutdown doesn't sit on them. Later calls
// do nothing.
func (s *Server) Drain() {
	s.drainOnce.Do(func() { close(s.draining) })
}

func (s *Server) registerWait(api huma.API) {
	waitOp := func(id, path, summary string) huma.Operation {
		o := op(id, http.MethodGet, path, summary, "Query", "")
		o.Description = "Waits for the branch to change, so a client can react to writes instead of polling. " +
			"Call it once without a cursor, then pass the cursor from each answer to the next call. " +
			"It answers as soon as the branch changes after that cursor, or after timeout seconds with changed false. " +
			"A change is a committed write that changed rows or the schema, or a reset, import, promotion or delete. " +
			"A cursor from before a restart, or from another branch, answers at once with changed true. " +
			"Needs read access. Waits don't count as queries in the branch's stats."
		return o
	}
	huma.Register(api, waitOp("wait", "/v1/databases/{db}/branches/{branch}/wait", "Wait for a branch to change"),
		func(ctx context.Context, in *waitIn) (*waitOut, error) {
			return s.wait(ctx, in.DB, in.Branch, in.Cursor, in.Timeout)
		})
	huma.Register(api, waitOp("wait-default", "/v1/databases/{db}/wait", "Wait for the default branch to change"),
		func(ctx context.Context, in *defaultWaitIn) (*waitOut, error) {
			return s.wait(ctx, in.DB, "", in.Cursor, in.Timeout)
		})
}

func (s *Server) wait(ctx context.Context, dbRef, branchRef, cursor string, timeoutSec int) (*waitOut, error) {
	p := principalFrom(ctx)
	d, b, err := s.branch(ctx, p, dbRef, branchRef, auth.AccessRead)
	if err != nil {
		return nil, toProblem(ctx, err)
	}
	if !s.waiters.take(p.TokenID) {
		return nil, toProblem(ctx, apierr.Newf(apierr.RateLimited, "this token already holds %d waits open", maxWaitsPerToken))
	}
	defer s.waiters.give(p.TokenID)

	timeout := time.Duration(min(timeoutSec, maxWaitSeconds)) * time.Second
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	select {
	case <-s.draining:
		timeout = 0
	default:
		go func() {
			select {
			case <-s.draining:
				cancel()
			case <-wctx.Done():
			}
		}()
	}
	t := engine.Target{Workspace: d.WorkspaceID, Database: d.ID, Branch: b.ID}
	w, err := s.eng.WaitChange(wctx, t, cursor, timeout)
	if err != nil {
		return nil, toProblem(ctx, err)
	}
	out := &waitOut{}
	out.Body.Cursor, out.Body.Changed = w.Cursor, w.Changed
	return out, nil
}
