// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// DB is the multi-agent database instance returned by Open. Business state
// (indices, active scenes, Dream bookkeeping, locks) lives in one
// agentContext per agent; llm/encoder/builtinCapabilities are stateless or
// connection-level and stay shared at the DB level.
type DB struct {
	engine  *core.StorageEngine
	config  *MemHopConfig
	llm     *Provider
	encoder Encoder
	// builtinCapabilities are read-only reference capabilities attached to
	// L5 query responses; they are never written to the file. Set once via
	// SetBuiltinCapabilities before the DB is published.
	builtinCapabilities []core.Capability

	closed atomic.Bool

	// baseCtx bounds every per-agent opCtx: Close cancels it so all
	// in-flight Dreams exit at their next stage boundary.
	baseCtx    context.Context
	baseCancel context.CancelFunc

	// agentsMu guards the agents registry, the snapshot blob cache and the
	// tenant name maps (nameToID/idToName).
	agentsMu      sync.Mutex
	agents        map[uint64]*agentContext
	snapshotBlobs map[uint64][]byte // sparse blobs of reclaimed agents
	nameToID      map[string]uint64 // tenant registry: name -> agentID
	idToName      map[uint64]string // tenant registry: agentID -> name

	// mu serializes Close against itself; per-operation domain locking is on
	// agentContext.mu instead of this DB-wide lock.
	mu sync.Mutex
}

// Lock/Unlock keep the combined write-lock seam for hosts; they map to the
// default agent domain.
func (db *DB) Lock() {
	ac, err := db.contextFor(core.DefaultAgentID)
	if err != nil {
		panic(err) // closed DB: same contract as the old unconditional lock
	}
	ac.mu.Lock()
}

func (db *DB) Unlock() {
	if ac := db.peekContext(core.DefaultAgentID); ac != nil {
		ac.mu.Unlock()
	}
}

func (db *DB) IsClosed() bool { return db.closed.Load() }

// contextFor returns the agent's context, creating it lazily on first
// access, and opportunistically sweeps idle domains. Non-default IDs must
// be registered tenants: a stale handle to a deleted agent never revives
// its domain.
func (db *DB) contextFor(agentID uint64) (*agentContext, error) {
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
		ac = db.newAgentContextLocked(agentID)
		db.agents[agentID] = ac
	}
	ac.lastActiveAt.Store(time.Now().UnixMilli())
	return ac, nil
}

// lockAgent takes the domain lock and re-checks the DeleteAgent tombstone
// under it: a handle that raced a deletion is rejected instead of writing
// into a tombstoned domain. Every business entry point must go through
// this helper.
func (db *DB) lockAgent(agentID uint64) (*agentContext, error) {
	ac, err := db.contextFor(agentID)
	if err != nil {
		return nil, err
	}
	ac.mu.Lock()
	if db.closed.Load() {
		// A caller that fetched its context before Close ran can still be
		// waiting here when the barrier passes and the engine shuts down:
		// reject instead of reporting success on a closed database.
		ac.mu.Unlock()
		return nil, common.NewError(common.ErrClosed, "database is closed")
	}
	if ac.deleted.Load() {
		ac.mu.Unlock()
		return nil, common.NewError(common.ErrAgentNotFound, "agent is being deleted")
	}
	return ac, nil
}

// lockSession is the shared prologue of the L6 session-scoped operations:
// take the domain lock, then parse the hex session id. On a parse failure
// the lock is released before returning, so callers add `defer ac.mu.Unlock()`
// only after the error check. It returns the locked context and the parsed
// session id.
func (db *DB) lockSession(agentID uint64, sessionID string) (*agentContext, uint64, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, 0, err
	}
	parsed, err := common.ParseID(sessionID)
	if err != nil {
		ac.mu.Unlock()
		return nil, 0, common.NewError(common.ErrInvalidQuery, "parse session id", err)
	}
	return ac, parsed, nil
}

// peekContext returns an existing context without creating one (nil when
// absent or the DB is closed).
func (db *DB) peekContext(agentID uint64) *agentContext {
	if db.closed.Load() {
		return nil
	}
	db.agentsMu.Lock()
	defer db.agentsMu.Unlock()
	return db.agents[agentID]
}

// newAgentContextLocked builds a context with its indices restored: the
// sparse index from the snapshot blob cache (empty for agents that never
// checkpointed), the L2Meta cache by scanning the agent's topic records.
// Caller must hold db.agentsMu.
func (db *DB) newAgentContextLocked(agentID uint64) *agentContext {
	ac := newAgentContext(agentID, db.baseCtx)
	ac.sparseIndex = index.NewSparseIndex()
	if blob := db.snapshotBlobs[agentID]; len(blob) > 0 {
		if idx, err := index.DeserializeSparseIndex(blob); err == nil {
			ac.sparseIndex = idx
		}
	}
	ac.l2Meta = index.BuildL2MetaFromEngine(db.engine, agentID)
	ac.traj = index.BuildTrajFromEngine(db.engine, agentID)
	return ac
}

// sweepIdleLocked reclaims contexts idle longer than Defaults.AgentIdleTTLMs:
// indices are dropped from memory, the domain's records stay on disk and
// the context rebuilds transparently on the next access. Domains whose lock
// is currently held (in-flight operation or scheduled Dream) and the default
// domain are never reclaimed. Caller must hold db.agentsMu.
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
		if now-ac.lastActiveAt.Load() <= ttl {
			continue
		}
		if !ac.mu.TryLock() { // an operation holds the domain lock: reclaim on a later pass
			continue
		}
		busy := len(ac.dreamInFlight) > 0
		var blob []byte
		var serr error
		if !busy {
			blob, serr = ac.sparseIndex.Serialize()
		}
		ac.mu.Unlock()
		if busy {
			continue
		}
		if serr != nil {
			// A reclaim that drops the live index on a serialize failure would
			// silently lose the domain's BM25 state (rebuild starts empty).
			// Keep the domain in memory and retry the sweep on a later pass.
			slog.Warn("agents: idle sweep skipped, sparse index serialize failed",
				"agent", common.FormatHash(id), "err", serr)
			continue
		}
		if len(blob) > 0 {
			db.snapshotBlobs[id] = blob
		}
		ac.opCancel()
		delete(db.agents, id)
	}
}

// destroyContext cancels the agent's cancellable work (Dreams and in-flight
// LLM calls), removes its context and drops
// the cached snapshot blob. Returns the destroyed context (nil when absent)
// so callers can wait for in-flight work if needed.
func (db *DB) destroyContext(agentID uint64) *agentContext {
	db.agentsMu.Lock()
	defer db.agentsMu.Unlock()
	ac := db.agents[agentID]
	if ac != nil {
		ac.opCancel()
		delete(db.agents, agentID)
	}
	delete(db.snapshotBlobs, agentID)
	return ac
}

// HasActiveScenes reports whether the default domain has active scenes
// (compatibility path used by the single-agent facade).
func (db *DB) HasActiveScenes() bool {
	return db.HasActiveScenesFor(core.DefaultAgentID)
}

// HasActiveScenesFor reports whether the agent domain has active scenes;
// a domain that was never materialized (or already reclaimed) has none.
func (db *DB) HasActiveScenesFor(agentID uint64) bool {
	ac := db.peekContext(agentID)
	if ac == nil {
		return false
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return len(ac.activeScenes) > 0
}

// loadTopicForWrite resolves a hex topic ID to its stored slot before a
// lifecycle write (Update / AppendL4Message / RefineTopicKeywords): a
// missing or unreadable topic is rejected with ErrNotFound so no orphan
// L4 archive or half-written refine is ever produced. Callers must hold
// ac.mu.
func (ac *agentContext) loadTopicForWrite(db *DB, topicID uint64) (*core.TopicSlot, error) {
	topics, err := repo.ListTopicsL2(repo.TopicListQuery{
		Engine:  db.engine,
		AgentID: ac.id,
		MetaIdx: ac.l2Meta,
		SceneID: topicID,
		Depth:   0,
		Num:     3,
	})
	if err != nil {
		return nil, err
	}
	if len(topics) == 0 {
		return nil, common.NewError(common.ErrNotFound, "topic not found")
	}
	topic := topics[0]
	return &topic, nil
}

// syncL2Meta refreshes one topic entry of the agent's L2MetaIndex from the
// record just written; call it right after engine writes, before the sparse
// index update (storage → l2meta → sparse lock order). On read failure the
// entry is removed so stale metadata is never served.
func (ac *agentContext) syncL2Meta(db *DB, idHash uint64) {
	if ac.l2Meta == nil {
		return
	}
	topic, err := core.ReadTopicLenient(db.engine, ac.id, idHash)
	if err != nil || topic == nil {
		ac.l2Meta.Remove(idHash)
		return
	}
	ac.l2Meta.Update(index.L2MetaFromTopic(topic))
}

// removeTopicsFromIndices drops the given topics from both the L2Meta cache
// and the BM25 sparse index of the agent's context; used by the DeleteScene /
// DeleteTopic paths after their records are tombstoned. Callers hold ac.mu.
func (ac *agentContext) removeTopicsFromIndices(ids []uint64) {
	for _, id := range ids {
		ac.l2Meta.Remove(id)
		ac.sparseIndex.RemoveDocument(id)
	}
}

// retargetL2Meta moves every topic of the merged-away scenes to the primary
// scene in the L2MetaIndex, mirroring repo.MergeScenesL2 after a merge.
func (ac *agentContext) retargetL2Meta(primaryHash uint64, removed map[uint64]struct{}) {
	if ac.l2Meta == nil {
		return
	}
	for sid := range removed {
		for _, id := range ac.l2Meta.GetByScene(sid) {
			meta := ac.l2Meta.Remove(id)
			if meta == nil {
				continue
			}
			meta.SceneID = primaryHash
			ac.l2Meta.Update(meta)
		}
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
	acs := make([]*agentContext, 0, len(db.agents))
	for _, ac := range db.agents {
		acs = append(acs, ac)
	}
	db.agentsMu.Unlock()
	for _, ac := range acs {
		ac.mu.Lock()
		ac.mu.Unlock() //nolint:staticcheck // barrier only
	}
	snap, err := db.buildSnapshot()
	if err != nil {
		return err
	}
	var encErr error
	if c, ok := db.encoder.(interface{ Close() error }); ok {
		encErr = c.Close()
	}
	// Always close the engine to release mmap/file even if the encoder failed.
	engErr := db.engine.Close(snap)
	if encErr != nil {
		return common.NewError(common.ErrEncoder, "encoder close", encErr)
	}
	return engErr
}

func (db *DB) Checkpoint() error {
	if db.closed.Load() {
		return common.NewError(common.ErrClosed, "database is closed")
	}
	snap, err := db.buildSnapshot()
	if err != nil {
		return err
	}
	return db.engine.Checkpoint(snap)
}

// buildSnapshot serializes the in-memory indices for checkpoint persistence:
// live contexts are serialized fresh; reclaimed contexts keep the blob they
// were serialized with at reclaim time, so no index data is lost.
func (db *DB) buildSnapshot() (*core.IndexSnapshotData, error) {
	db.agentsMu.Lock()
	defer db.agentsMu.Unlock()
	blobs := make(map[uint64][]byte, len(db.snapshotBlobs)+len(db.agents))
	for id, blob := range db.snapshotBlobs {
		blobs[id] = blob
	}
	for id, ac := range db.agents {
		data, err := ac.sparseIndex.Serialize()
		if err != nil {
			return nil, common.NewError(common.ErrSerialization, "sparse index", err)
		}
		blobs[id] = data
		delete(db.snapshotBlobs, id)
	}
	return &core.IndexSnapshotData{SparseByAgent: blobs}, nil
}
