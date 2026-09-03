// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/llm"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// DB is the multi-agent database instance returned by Open. Business state
// (L2Meta cache, Dream bookkeeping, locks) lives in one domain.Context per
// agent; llm/builtinCapabilities are stateless or connection-level and stay
// shared at the DB level.
type DB struct {
	engine *core.StorageEngine
	config *MemHopConfig
	llm    *llm.Provider
	// builtinCapabilities are read-only reference capabilities attached to
	// L5 query responses; they are never written to the file. Set once via
	// SetBuiltinCapabilities before the DB is published.
	builtinCapabilities []core.Capability

	closed atomic.Bool

	// baseCtx bounds every per-agent opCtx: Close cancels it so all
	// in-flight Dreams exit at their next stage boundary.
	baseCtx    context.Context
	baseCancel context.CancelFunc

	// agentsMu guards the agents registry and the tenant name maps
	// (nameToID/idToName).
	agentsMu sync.Mutex
	agents   map[uint64]*domain.Context
	nameToID map[string]uint64 // tenant registry: name -> agentID
	idToName map[uint64]string // tenant registry: agentID -> name

	// mu serializes Close against itself; per-operation domain locking is on
	// domain.Context.Mu instead of this DB-wide lock.
	mu sync.Mutex
}

// Lock/Unlock keep the combined write-lock seam for hosts; they map to the
// default agent domain.
func (db *DB) Lock() {
	ac, err := db.contextFor(core.DefaultAgentID)
	if err != nil {
		panic(err) // closed DB: same contract as the old unconditional lock
	}
	ac.Mu.Lock()
}

func (db *DB) Unlock() {
	if ac := db.peekContext(core.DefaultAgentID); ac != nil {
		ac.Mu.Unlock()
	}
}

func (db *DB) IsClosed() bool { return db.closed.Load() }

// contextFor returns the agent's context, creating it lazily on first
// access, and opportunistically sweeps idle domains. Non-default IDs must
// be registered tenants: a stale handle to a deleted agent never revives
// its domain.
func (db *DB) contextFor(agentID uint64) (*domain.Context, error) {
	if db.closed.Load() {
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	db.agentsMu.Lock()
	defer db.agentsMu.Unlock()
	if db.closed.Load() { // re-check under the lock: Close may have raced the check above
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	if agentID != core.DefaultAgentID {
		if _, ok := db.idToName[agentID]; !ok {
			return nil, common.NewError(common.ErrAgentNotFound, "agent is not registered")
		}
	}
	db.sweepIdleLocked()
	ac := db.agents[agentID]
	if ac == nil {
		ac = domain.NewContext(agentID, db.baseCtx, db.engine, db.llm, &db.config.Defaults)
		db.agents[agentID] = ac
	}
	ac.LastActiveAt.Store(time.Now().UnixMilli())
	return ac, nil
}

// lockAgent takes the domain lock and re-checks the DeleteAgent tombstone
// under it: a handle that raced a deletion is rejected instead of writing
// into a tombstoned domain. Every business entry point must go through
// this helper.
func (db *DB) lockAgent(agentID uint64) (*domain.Context, error) {
	ac, err := db.contextFor(agentID)
	if err != nil {
		return nil, err
	}
	ac.Mu.Lock()
	if db.closed.Load() {
		// A caller that fetched its context before Close ran can still be
		// waiting here when the barrier passes and the engine shuts down:
		// reject instead of reporting success on a closed database.
		ac.Mu.Unlock()
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	if ac.Deleted.Load() {
		ac.Mu.Unlock()
		return nil, common.NewError(common.ErrAgentNotFound, "agent is being deleted")
	}
	return ac, nil
}

// lockSession is the shared prologue of the L6 session-scoped operations:
// take the domain lock, then parse the hex session id. On a parse failure
// the lock is released before returning, so callers add `defer ac.Mu.Unlock()`
// only after the error check. It returns the locked context and the parsed
// session id.
func (db *DB) lockSession(agentID uint64, sessionID string) (*domain.Context, uint64, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, 0, err
	}
	parsed, err := common.ParseID(sessionID)
	if err != nil {
		ac.Mu.Unlock()
		return nil, 0, common.NewError(common.ErrInvalidQuery, "parse session id", err)
	}
	return ac, parsed, nil
}

// peekContext returns an existing context without creating one (nil when
// absent or the DB is closed).
func (db *DB) peekContext(agentID uint64) *domain.Context {
	if db.closed.Load() {
		return nil
	}
	db.agentsMu.Lock()
	defer db.agentsMu.Unlock()
	return db.agents[agentID]
}

// sweepIdleLocked reclaims contexts idle longer than Defaults.AgentIdleTTLMs.
// Nothing is persisted at reclaim time: the dropped L2Meta cache rebuilds from
// the agent's records on the next access. Domains whose lock is currently held
// (in-flight operation or scheduled Dream) and the default domain are never
// reclaimed. Caller must hold db.agentsMu.
func (db *DB) sweepIdleLocked() {
	ttl := db.config.Defaults.AgentIdleTTLMs
	if ttl <= 0 {
		return
	}
	now := time.Now().UnixMilli()
	for id, ac := range db.agents {
		if id == core.DefaultAgentID {
			continue
		}
		if now-ac.LastActiveAt.Load() <= ttl {
			continue
		}
		if !ac.Mu.TryLock() { // an operation holds the domain lock: reclaim on a later pass
			continue
		}
		busy := len(ac.DreamInFlight) > 0
		ac.Mu.Unlock()
		if busy {
			continue
		}
		ac.OpCancel()
		delete(db.agents, id)
	}
}

// destroyContext cancels the agent's cancellable work (Dreams and in-flight
// LLM calls) and removes its context. Returns the destroyed context (nil when
// absent) so callers can wait for in-flight work if needed.
func (db *DB) destroyContext(agentID uint64) *domain.Context {
	db.agentsMu.Lock()
	defer db.agentsMu.Unlock()
	ac := db.agents[agentID]
	if ac != nil {
		ac.OpCancel()
		delete(db.agents, agentID)
	}
	return ac
}

func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if !db.closed.CompareAndSwap(false, true) {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	// Cancel background Dreams so an in-flight pipeline exits at its next
	// stage boundary; then wait for every domain lock so no operation is
	// mid-write when the engine closes.
	db.baseCancel()
	db.agentsMu.Lock()
	acs := make([]*domain.Context, 0, len(db.agents))
	for _, ac := range db.agents {
		acs = append(acs, ac)
	}
	db.agentsMu.Unlock()
	for _, ac := range acs {
		ac.Mu.Lock()
		ac.Mu.Unlock() //nolint:staticcheck // barrier only
	}
	return db.engine.Close(nil)
}

func (db *DB) Checkpoint() error {
	if db.closed.Load() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return db.engine.Checkpoint(nil)
}
