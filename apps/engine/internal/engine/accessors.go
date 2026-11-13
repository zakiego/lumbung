package engine

import (
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// Meta returns meta.db, for the API's tokens, audit and stats.
func (e *Engine) Meta() *meta.Store { return e.meta }

// Config returns the engine's configuration.
func (e *Engine) Config() config.Config { return e.cfg }

// BranchBytes is the space a branch takes on disk, file plus WAL.
func (e *Engine) BranchBytes(branchID string) int64 {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	return e.sizes[branchID]
}
