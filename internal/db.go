// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/content"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/llm"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// DB is the multi-agent database instance returned by Open. Business state
// (L2Meta cache, Dream bookkeeping, locks) lives in one domain.Context per
// agent. The LLM transport is shared by every domain unless one was given its
// own endpoint when it was created.
type DB struct {
	engine *core.StorageEngine
	config *MemHopConfig
	llm    *llm.Provider

	closed atomic.Bool

	// baseCtx bounds every per-agent opCtx: Close cancels it so all
	// in-flight Dreams exit at their next stage boundary.
	baseCtx    context.Context
	baseCancel context.CancelFunc

	// agentsMu guards the agents registry, the tenant name maps
	// (nameToID/idToName) and the two LLM tables below.
	agentsMu sync.Mutex
	agents   map[uint64]*domain.Context
	nameToID map[string]uint64 // tenant registry: name -> agentID
	idToName map[uint64]string // tenant registry: agentID -> name

	// llmByAgent is one domain's own endpoint, and providers dedupes
	// transports by config value so a hundred sub-agents sharing an endpoint
	// share one http.Client. Both deliberately outlive the domain contexts:
	// the idle sweep drops a context and contextFor rebuilds it, so an override
	// stored on the context would quietly fall back to the library-wide
	// endpoint once a domain went idle long enough.
	llmByAgent map[uint64]*llm.Provider
	providers  map[LlmConfig]*llm.Provider

	// mu serializes Close against itself; per-operation domain locking is on
	// domain.Context.Mu instead of this DB-wide lock.
	mu sync.Mutex
}

func (db *DB) IsClosed() bool { return db.closed.Load() }

// contextFor returns the agent's context, creating it lazily on first
// access, and opportunistically sweeps idle domains. Non-default IDs must
// be registered tenants, so an id nobody ever issued cannot open a domain of
// its own. The reserved shared-pool domain is exempt from the registry
// check: it has no tenant record and is created on first L3 access.
func (db *DB) contextFor(agentID uint64) (*domain.Context, error) {
	if db.closed.Load() {
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	db.agentsMu.Lock()
	defer db.agentsMu.Unlock()
	if db.closed.Load() { // re-check under the lock: Close may have raced the check above
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	if agentID != core.DefaultAgentID && agentID != core.SharedPoolAgentID {
		if _, ok := db.idToName[agentID]; !ok {
			return nil, common.NewError(common.ErrAgentNotFound, "agent is not registered")
		}
	}
	db.sweepIdleLocked()
	ac := db.agents[agentID]
	if ac == nil {
		// A rebuild after the idle sweep lands here too, which is why the
		// endpoint override lives in its own table rather than on the context.
		chat := db.llm
		if own, ok := db.llmByAgent[agentID]; ok {
			chat = own
		}
		ac = domain.NewContext(agentID, db.baseCtx, db.engine, chat, &db.config.Defaults)
		db.agents[agentID] = ac
	}
	ac.LastActiveAt.Store(time.Now().UnixMilli())
	return ac, nil
}

// providerForLocked returns the transport for one endpoint, building it on
// first use. Sub-agents are created per tenant and a process can hold many of
// them against the same endpoint, so the config value is the dedupe key: one
// http.Client per endpoint rather than one per domain. Caller holds agentsMu.
func (db *DB) providerForLocked(cfg LlmConfig) *llm.Provider {
	if p, ok := db.providers[cfg]; ok {
		return p
	}
	p := llm.New(cfg)
	db.providers[cfg] = p
	return p
}

// setDomainLLM points one domain at its own endpoint. A later call for the same
// domain replaces it, which is what a reconnecting host wants: the endpoint it
// names now is the one its turns use from here on.
func (db *DB) setDomainLLM(agentID uint64, cfg LlmConfig) {
	db.agentsMu.Lock()
	defer db.agentsMu.Unlock()
	db.llmByAgent[agentID] = db.providerForLocked(cfg)
}

// lockAgent takes the domain lock and re-checks under it that the database is
// still open: a caller that fetched its context before Close ran can still be
// waiting here when the barrier passes. Every business entry point must go
// through this helper.
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
	return ac, nil
}

// lockSession is the shared prologue of the turn-keyed operations: take the domain
// lock, then parse the turn's topic id. On a parse failure the lock is released
// before returning, so callers add `defer ac.Mu.Unlock()` only after the error
// check. It returns the locked context and the parsed key. The parse is the one
// every turn-keyed entry uses (content.ParseTopicID), so a reserved all-zero key is
// refused the same way wherever a host can hand one in.
func (db *DB) lockSession(agentID uint64, sessionID string) (*domain.Context, uint64, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, 0, err
	}
	parsed, err := content.ParseTopicID(sessionID)
	if err != nil {
		ac.Mu.Unlock()
		return nil, 0, err
	}
	return ac, parsed, nil
}

// lockSharedPool is the prologue of every L3 operation: the caller's own
// domain must still be alive (a stale handle to a deleted agent must not
// keep using the shared pool), then the shared pool domain is locked. The
// L3 records live in the file-wide shared domain, so shared-pool
// operations from different agents serialize on its lock. The shared domain
// is never deleted, so no tombstone re-check is needed. The caller check is
// point-in-time: a caller deleted mid-operation lets the call run to
// completion, which is harmless — it touches only the shared pool, and
// DeleteL3's anchor detach skips domains it can no longer lock.
func (db *DB) lockSharedPool(callerID uint64) (*domain.Context, error) {
	if err := db.CheckSession(callerID); err != nil {
		return nil, err
	}
	ac, err := db.contextFor(core.SharedPoolAgentID)
	if err != nil {
		return nil, err
	}
	ac.Mu.Lock()
	if db.closed.Load() {
		ac.Mu.Unlock()
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	return ac, nil
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
		if id == core.DefaultAgentID || id == core.SharedPoolAgentID {
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
	return db.engine.Close()
}

func (db *DB) Checkpoint() error {
	if db.closed.Load() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	return db.engine.Checkpoint()
}

// CompactTo writes a defragmented copy of the database at newPath: only live
// records, in one fresh log, with that copy's own rebuilt index. Deletes are
// tombstones — removing a scene or a graph frees no bytes until a
// compaction rewrites the log — so this is the space-reclamation entry the
// lifecycle surface otherwise lacks.
//
// It never touches the open file. newPath must not exist yet, which keeps the
// copy from being destroyed by the rewrite and leaves the swap (close, rename,
// reopen) to the host's own backup policy. The copy is a point-in-time snapshot:
// records appended while it is being written are not in it, so compact when the
// domain is quiet — typically just before Close.
func (db *DB) CompactTo(newPath string) error {
	if db.closed.Load() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	if newPath == "" {
		return common.NewError(common.ErrInvalidQuery, "compact: newPath is required")
	}
	if sameFile(newPath, db.config.DBPath) {
		return common.NewError(common.ErrInvalidQuery, "compact: newPath must differ from the open database file")
	}
	if _, err := os.Stat(newPath); err == nil {
		return common.NewError(common.ErrInvalidQuery, "compact: newPath already exists: "+newPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return common.NewError(common.ErrIO, "compact: stat newPath", err)
	}
	return db.engine.Compact(newPath)
}

// sameFile compares two paths for the engine-level check that a compaction
// never targets the file it is reading.
func sameFile(a, b string) bool {
	abs := func(p string) string {
		full, err := filepath.Abs(p)
		if err != nil {
			return filepath.Clean(p)
		}
		return full
	}
	return abs(a) == abs(b)
}
