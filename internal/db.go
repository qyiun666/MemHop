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

	// llmByAgent holds one domain's own endpoint override; providers dedupes
	// transports by config value so domains sharing an endpoint share one
	// http.Client. Both outlive the domain contexts deliberately: the idle
	// sweep drops a context and contextFor rebuilds it, so an override stored
	// on the context would quietly fall back to the library-wide endpoint.
	llmByAgent map[uint64]*llm.Provider
	providers  map[LlmConfig]*llm.Provider

	// mu serializes Close against itself; per-operation domain locking is on
	// domain.Context.Mu instead of this DB-wide lock.
	mu sync.Mutex
}

// errDBClosed is what every call on a closed database answers, in so many words: the
// composition root checks the same flag at four different points in its lock protocol, and
// a host reading CodeOf must not be able to tell which one it hit. TestEveryCallAnswersErrClosedAfterClose
// walks the whole published surface after a Close to keep that promise.
var errDBClosed = common.NewError(common.ErrClosed, "database is closed")

func (db *DB) IsClosed() bool { return db.closed.Load() }

// contextFor returns the agent's context, creating it lazily on first access,
// and opportunistically sweeps idle domains. Non-default IDs must be
// registered tenants. The reserved shared-pool domain is exempt from the
// registry check: it has no tenant record and is created on first L3 access.
func (db *DB) contextFor(agentID uint64) (*domain.Context, error) {
	if db.closed.Load() {
		return nil, errDBClosed
	}
	db.agentsMu.Lock()
	defer db.agentsMu.Unlock()
	if db.closed.Load() { // re-check under the lock: Close may have raced the check above
		return nil, errDBClosed
	}
	if agentID != core.DefaultAgentID && agentID != core.SharedPoolAgentID {
		if _, ok := db.idToName[agentID]; !ok {
			return nil, common.NewError(common.ErrAgentNotFound, "agent is not registered")
		}
	}
	db.sweepIdleLocked()
	ac := db.agents[agentID]
	if ac == nil {
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
// first use; the config value is the dedupe key. Caller holds agentsMu.
func (db *DB) providerForLocked(cfg LlmConfig) *llm.Provider {
	if p, ok := db.providers[cfg]; ok {
		return p
	}
	p := llm.New(cfg)
	db.providers[cfg] = p
	return p
}

// setDomainLLM records one domain's own endpoint and hands back the transport
// it names. The table is only read when a context is built, so a domain whose
// context is still live has to be re-pointed by its caller under the domain
// lock.
func (db *DB) setDomainLLM(agentID uint64, cfg LlmConfig) *llm.Provider {
	db.agentsMu.Lock()
	defer db.agentsMu.Unlock()
	provider := db.providerForLocked(cfg)
	db.llmByAgent[agentID] = provider
	return provider
}

// lockAgent takes the domain lock, rejects a closed database under it
// (lockOpen), and re-checks that this context is still the domain's: a caller
// that waited longer than the idle TTL before getting the lock can find the
// domain reclaimed. Every business entry point must go through this helper.
func (db *DB) lockAgent(agentID uint64) (*domain.Context, error) {
	for {
		ac, err := db.contextFor(agentID)
		if err != nil {
			return nil, err
		}
		if err := db.lockOpen(ac); err != nil {
			return nil, err
		}
		if ac.Reclaimed.Load() {
			// Reclaimed under this lock, so running here would be an operation on a
			// domain the table no longer holds. Fetching again cannot loop: a sweep
			// runs before the context it hands back is stamped as just active.
			ac.Mu.Unlock()
			continue
		}
		return ac, nil
	}
}

// lockOpen takes the domain lock and, under it, rejects a closed database: a
// caller that fetched its context before Close ran can still be waiting here
// when the barrier passes, and reporting success then would operate on a shut
// engine. On error no lock is held.
func (db *DB) lockOpen(ac *domain.Context) error {
	ac.Mu.Lock()
	if db.closed.Load() {
		ac.Mu.Unlock()
		return errDBClosed
	}
	return nil
}

// lockTurn is the prologue of every call that closes or extends the turn Search
// opened: the domain lock, then a refusal when the domain holds no open turn.
// Guessing one — the newest scene's next turn — would write a closing line onto a
// turn nobody opened, so the host hears that it has to read first.
func (db *DB) lockTurn(agentID uint64) (*domain.Context, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	if ac.Turn == 0 {
		ac.Mu.Unlock()
		return nil, common.NewError(common.ErrInvalidQuery,
			"no turn is open: Search opens the turn a call like this one closes")
	}
	return ac, nil
}

// lockSharedPool is the prologue of every L3 operation: the caller's own
// domain must still be alive (a stale handle to a deleted agent must not keep
// using the shared pool), then the shared pool domain is locked. The L3
// records live in the file-wide shared domain, so shared-pool operations from
// different agents serialize on its lock. The shared domain is never deleted
// and exempt from the idle sweep, so the reclaim re-check lockAgent does
// cannot trigger here. The caller check is point-in-time: a caller deleted
// mid-operation lets the call run to completion, which is harmless — it
// touches only the shared pool, and DeleteL3's anchor detach skips domains it
// can no longer lock.
func (db *DB) lockSharedPool(callerID uint64) (*domain.Context, error) {
	if err := db.CheckSession(callerID); err != nil {
		return nil, err
	}
	ac, err := db.contextFor(core.SharedPoolAgentID)
	if err != nil {
		return nil, err
	}
	if err := db.lockOpen(ac); err != nil {
		return nil, err
	}
	return ac, nil
}

// sweepIdleLocked reclaims contexts idle longer than Defaults.AgentIdleTTLMs.
// Nothing is persisted at reclaim time: the dropped L2Meta cache rebuilds from
// the agent's records on the next access. Domains whose lock is currently held
// (in-flight operation or scheduled Dream), the default domain and the shared
// pool are never reclaimed. Caller must hold db.agentsMu.
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
		if len(ac.DreamInFlight) > 0 {
			ac.Mu.Unlock()
			continue
		}
		// Marking and removal sit inside the lock hold, and lockAgent re-checks
		// the mark under the same lock: a caller that queued behind an operation
		// past the TTL fetches the live domain instead of running on this one.
		ac.Reclaimed.Store(true)
		ac.OpCancel()
		delete(db.agents, id)
		ac.Mu.Unlock()
	}
}

func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if !db.closed.CompareAndSwap(false, true) {
		return errDBClosed
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
		return errDBClosed
	}
	return db.engine.Checkpoint()
}

// Stats reports the file-level diagnostics: the size of the .meh file in bytes
// and the number of live records across every domain of the file. Read-only and
// scoped to the engine's own mutex — no domain lock is taken, so it answers
// while domains are busy.
func (db *DB) Stats() (int64, int, error) {
	if db.closed.Load() {
		return 0, 0, errDBClosed
	}
	size, records := db.engine.Stats()
	return size, records, nil
}

// CompactTo writes a defragmented copy of the database at newPath: only live
// records, in one fresh log, with that copy's own rebuilt index. Deletes are
// tombstones, so this is the space-reclamation entry the lifecycle surface
// otherwise lacks.
//
// It never touches the open file, and newPath must not exist yet — the swap
// (close, rename, reopen) is left to the host's own backup policy. The copy
// is a point-in-time snapshot: records appended while it is being written are
// not in it, so compact when the domain is quiet — typically just before Close.
func (db *DB) CompactTo(newPath string) error {
	if db.closed.Load() {
		return errDBClosed
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

// sameFile reports whether two paths name the same file.
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
