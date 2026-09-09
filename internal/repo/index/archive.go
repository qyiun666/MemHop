// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// ArchiveIndex caches which L4 records belong to which topic, so a scene read
// addresses a turn's originals by the topic's own id. It replaces the reference
// list topics used to carry: an archive id hashes its own text, so it cannot be
// derived from the topic and cannot be enumerated without this cache.
//
// Entries are ordered by CreatedAt ascending, which is the order a transcript
// reads back in.
package index

import (
	"cmp"
	"slices"
	"sync"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

type archiveEntry struct {
	IDHash    uint64
	CreatedAt int64
}

type ArchiveIndex struct {
	mu      sync.RWMutex
	byTopic map[uint64][]archiveEntry
}

func NewArchiveIndex() *ArchiveIndex {
	return &ArchiveIndex{byTopic: make(map[uint64][]archiveEntry)}
}

// BuildArchiveFromEngine scans one agent domain's L4 records into a fresh
// index. Corrupt or unparsable records are skipped, the same tolerance every
// other rebuild has: a torn tail must not make the domain unopenable.
func BuildArchiveFromEngine(engine *core.StorageEngine, agentID uint64) *ArchiveIndex {
	idx := NewArchiveIndex()
	for _, arc := range core.CollectAllArchives(engine, agentID) {
		idx.Append(arc.ContextID, arc.IDHash, arc.CreatedAt)
	}
	return idx
}

// Append records one archive under its topic. Re-appending an id already
// listed is a no-op: an id hashes its content, so settling the same turn twice
// with the same texts lands on one record, and a doubled entry would render
// that utterance twice.
func (idx *ArchiveIndex) Append(topicID, idHash uint64, createdAt int64) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	entries := idx.byTopic[topicID]
	for _, e := range entries {
		if e.IDHash == idHash {
			return
		}
	}
	entries = append(entries, archiveEntry{IDHash: idHash, CreatedAt: createdAt})
	slices.SortStableFunc(entries, func(a, b archiveEntry) int { return cmp.Compare(a.CreatedAt, b.CreatedAt) })
	idx.byTopic[topicID] = entries
}

// Hashes returns a topic's archive ids in CreatedAt order; nil for a topic with
// no archived content, which is a topic nobody has settled yet, not an error.
func (idx *ArchiveIndex) Hashes(topicID uint64) []uint64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	entries := idx.byTopic[topicID]
	if len(entries) == 0 {
		return nil
	}
	out := make([]uint64, len(entries))
	for i, e := range entries {
		out[i] = e.IDHash
	}
	return out
}

// RemoveIDs drops specific archives of one topic — the counterpart of deleting
// those records — and returns how many went away. Leaving an entry that names a
// deleted record makes every later read of that topic fail on a record that no
// longer exists.
func (idx *ArchiveIndex) RemoveIDs(topicID uint64, idHashes []uint64) int {
	doomed := make(map[uint64]struct{}, len(idHashes))
	for _, h := range idHashes {
		doomed[h] = struct{}{}
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	entries := idx.byTopic[topicID]
	if len(entries) == 0 {
		return 0
	}
	kept := entries[:0]
	removed := 0
	for _, e := range entries {
		if _, ok := doomed[e.IDHash]; ok {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	if removed == 0 {
		return 0
	}
	if len(kept) == 0 {
		delete(idx.byTopic, topicID)
		return removed
	}
	idx.byTopic[topicID] = kept
	return removed
}

// RemoveTopic drops a whole topic's entries, the counterpart of deleting all
// the archives it owned.
func (idx *ArchiveIndex) RemoveTopic(topicID uint64) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.byTopic, topicID)
}
