// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// TrajIndex caches the per-turn shape of an agent's L6 trajectory log so
// Append/Read/List avoid scanning and JSON-parsing every stored
// event. Built from records when the agent context is created and maintained
// incrementally by the internal layer, which owns every trajectory write and
// delete under the same domain lock.
package index

import (
	"cmp"
	"slices"
	"sync"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

type trajEntry struct {
	Seq       uint64
	IDHash    uint64
	Timestamp int64
}

type trajTurn struct {
	entries []trajEntry // Seq ascending
}

// TrajSummary is one turn's footprint in the index.
type TrajSummary struct {
	SessionID uint64
	Steps     int
	LastAt    int64
}

type TrajIndex struct {
	mu       sync.RWMutex
	byTurn   map[uint64]*trajTurn
	lastTurn map[uint64]trajEntry // cache of each turn's newest entry (Seq max)
}

func NewTrajIndex() *TrajIndex {
	return &TrajIndex{
		byTurn:   make(map[uint64]*trajTurn),
		lastTurn: make(map[uint64]trajEntry),
	}
}

// BuildTrajFromEngine scans one agent domain's trajectory records into a
// fresh index; corrupt or unparsable records are skipped (tolerated torn
// residue). The engine scan yields records in map order, so they are sorted
// into (turn, Seq) order first — every index reader assumes entries land
// Seq-ascending per turn.
func BuildTrajFromEngine(engine *core.StorageEngine, agentID uint64) *TrajIndex {
	idx := NewTrajIndex()
	evs := core.CollectAllTrajectories(engine, agentID)
	slices.SortFunc(evs, func(a, b core.TrajectorySlot) int {
		return cmp.Or(cmp.Compare(a.SessionID, b.SessionID), cmp.Compare(a.Seq, b.Seq))
	})
	for _, ev := range evs {
		if ev.NodeType == core.NodeTypePlan {
			continue // plan nodes are not per-turn events; they are read via CollectPlanNodes
		}
		idx.Append(ev.SessionID, ev.Seq, ev.IDHash, ev.Timestamp)
	}
	return idx
}

// Append records one event; entries stay Seq-ascending per turn.
func (idx *TrajIndex) Append(sessionID, seq, idHash uint64, ts int64) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	turn := idx.byTurn[sessionID]
	if turn == nil {
		turn = &trajTurn{}
		idx.byTurn[sessionID] = turn
	}
	turn.entries = append(turn.entries, trajEntry{Seq: seq, IDHash: idHash, Timestamp: ts})
	idx.lastTurn[sessionID] = turn.entries[len(turn.entries)-1]
}

// MaxSeq returns the turn's highest known Seq (ok=false when the turn is
// unknown, i.e. its first Append).
func (idx *TrajIndex) MaxSeq(sessionID uint64) (uint64, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	e, ok := idx.lastTurn[sessionID]
	return e.Seq, ok
}

// EventHashes returns the turn's event ids in Seq order; nil for unknown
// turns.
func (idx *TrajIndex) EventHashes(sessionID uint64) []uint64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	turn := idx.byTurn[sessionID]
	if turn == nil {
		return nil
	}
	out := make([]uint64, len(turn.entries))
	for i, e := range turn.entries {
		out[i] = e.IDHash
	}
	return out
}

// RemoveBefore drops events strictly older than before (Unix ms) across all
// turns and returns the removed event ids; emptied turns are deleted.
func (idx *TrajIndex) RemoveBefore(before int64) []uint64 {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	var out []uint64
	for sid, turn := range idx.byTurn {
		kept := turn.entries[:0]
		for _, e := range turn.entries {
			if e.Timestamp < before {
				out = append(out, e.IDHash)
				continue
			}
			kept = append(kept, e)
		}
		if len(kept) == 0 {
			delete(idx.byTurn, sid)
			delete(idx.lastTurn, sid)
			continue
		}
		turn.entries = kept
		idx.lastTurn[sid] = kept[len(kept)-1]
	}
	return out
}

// RemoveSession drops one whole turn and returns its event ids in Seq
// order; nil for unknown turns (idempotent). Used by PlanReplace, which
// removes a plan's bound events wholesale and restarts its Seq space.
func (idx *TrajIndex) RemoveSession(sessionID uint64) []uint64 {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	turn := idx.byTurn[sessionID]
	if turn == nil {
		return nil
	}
	out := make([]uint64, len(turn.entries))
	for i, e := range turn.entries {
		out[i] = e.IDHash
	}
	delete(idx.byTurn, sessionID)
	delete(idx.lastTurn, sessionID)
	return out
}

// Summaries returns every turn's footprint (unordered).
func (idx *TrajIndex) Summaries() []TrajSummary {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make([]TrajSummary, 0, len(idx.byTurn))
	for sid, turn := range idx.byTurn {
		s := TrajSummary{SessionID: sid, Steps: len(turn.entries)}
		if len(turn.entries) > 0 {
			s.LastAt = turn.entries[len(turn.entries)-1].Timestamp
		}
		out = append(out, s)
	}
	return out
}
