package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// idempotencyTTL is how long an Idempotency-Key is remembered.
const idempotencyTTL = 24 * time.Hour

// keyLocks serializes requests that share a (token, key), and only those.
var keyLocks = struct {
	sync.Mutex
	m map[string]*keyLock
}{m: map[string]*keyLock{}}

type keyLock struct {
	sync.Mutex
	users int
}

func lockKey(k string) func() {
	keyLocks.Lock()
	l, ok := keyLocks.m[k]
	if !ok {
		l = &keyLock{}
		keyLocks.m[k] = l
	}
	l.users++
	keyLocks.Unlock()
	l.Lock()
	return func() {
		l.Unlock()
		keyLocks.Lock()
		l.users--
		if l.users == 0 {
			delete(keyLocks.m, k)
		}
		keyLocks.Unlock()
	}
}

// idempotent runs a create once per (token, Idempotency-Key). A retry with
// the same key and the same request gets the stored response; the same key
// with a different request is refused. request must include everything that
// makes the request distinct, path parameters too. Without a key it just
// runs f. Never use it for a response holding a secret: it is stored.
func idempotent[T any](ctx context.Context, s *Server, opID, key string, request any, f func() (*T, error)) (*T, error) {
	if key == "" {
		return f()
	}
	p := principalFrom(ctx)
	reqJSON, _ := json.Marshal(request)
	sum := sha256.Sum256(append([]byte(opID+"\x00"), reqJSON...))

	defer lockKey(p.TokenID + "\x00" + key)()
	prev, ok, err := s.store.GetIdempotent(ctx, p.TokenID, key, idempotencyTTL)
	if err != nil {
		return nil, toProblem(ctx, err)
	}
	if ok {
		if !bytes.Equal(prev.RequestHash, sum[:]) {
			return nil, toProblem(ctx, apierr.New(apierr.IdempotencyMismatch, "this Idempotency-Key was used for a different request"))
		}
		var out T
		if err := json.Unmarshal(prev.Body, &out); err != nil {
			return nil, toProblem(ctx, err)
		}
		setTarget(ctx, "replay of Idempotency-Key "+key)
		return &out, nil
	}
	out, err := f()
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, toProblem(ctx, err)
	}
	if err := s.store.PutIdempotent(ctx, p.TokenID, key, meta.Idempotent{Method: opID, Path: opID, RequestHash: sum[:], Status: 0, Body: body}, idempotencyTTL); err != nil {
		s.log.Error("store idempotency key", "err", err)
	}
	return out, nil
}

// lastActivity is the latest minute with a query on any of the branches,
// counting stats not yet flushed.
func (s *Server) lastActivity(ctx context.Context, branchIDs []string) *time.Time {
	latest, err := s.store.LastActivity(ctx, branchIDs)
	if err != nil {
		s.log.Error("last activity", "err", err)
	}
	for _, id := range branchIDs {
		for _, b := range s.stats.Pending(id) {
			if latest == nil || b.Minute.After(*latest) {
				m := b.Minute
				latest = &m
			}
		}
	}
	return latest
}
