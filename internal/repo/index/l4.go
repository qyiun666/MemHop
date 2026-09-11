// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4Index caches one agent domain's content inventory grouped by topic, so a
// read can enumerate what a turn holds. A single record is addressable as
// (topic, Seq) without any index — that is what its id hashes — but listing a
// topic's Seqs is exactly what this cache is for, and it is the only list of
// what a topic owns.
//
// Entries are ordered by Seq ascending. Seq is one space per topic shared by
// both kinds, and it is a total order: utterances hold 1 and 2, events are
// allocated above them in append order. So Seq alone gives a transcript its
// question-first reading and an event log its chronology, with no tie-break on
// timestamp or role.
//
// Kind is part of every entry, not decoration: without it a turn that holds
// nothing but its two originals reports as a turn with a trajectory, and a scene
// read pulls back dozens of events for a two-line conversation.
package index

import (
	"cmp"
	"slices"
	"sync"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

type l4Entry struct {
	Seq       uint64
	IDHash    uint64
	Kind      core.ArchiveKind
	CreatedAt int64
}

type L4Index struct {
	mu      sync.RWMutex
	byTopic map[uint64][]l4Entry // Seq ascending; one entry per (topic, Seq)
}

func NewL4Index() *L4Index {
	return &L4Index{byTopic: make(map[uint64][]l4Entry)}
}

// BuildL4FromEngine scans one agent domain's L4 records into a fresh index.
// Corrupt or unparsable records are skipped, the tolerance every rebuild shares:
// a torn tail must not make the domain unopenable.
func BuildL4FromEngine(engine *core.StorageEngine, agentID uint64) *L4Index {
	idx := NewL4Index()
	for _, arc := range core.CollectAllArchives(engine, agentID) {
		idx.Append(arc.TopicID, arc.Seq, arc.IDHash, arc.Kind, arc.CreatedAt)
	}
	return idx
}

// Append records one content slot, replacing whatever held that Seq: re-writing
// a Seq is an in-place overwrite on the disk too, so the mirror must not grow.
// An utterance can land after the events of its turn (a caller records events
// while it runs and settles the turn afterwards), so entries insert in Seq
// order rather than at the tail.
func (idx *L4Index) Append(topicID, seq, idHash uint64, kind core.ArchiveKind, createdAt int64) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	entries := idx.byTopic[topicID]
	at, _ := slices.BinarySearchFunc(entries, seq, func(e l4Entry, s uint64) int {
		return cmp.Compare(e.Seq, s)
	})
	if at < len(entries) && entries[at].Seq == seq {
		entries[at] = l4Entry{Seq: seq, IDHash: idHash, Kind: kind, CreatedAt: createdAt}
		return
	}
	entries = slices.Insert(entries, at, l4Entry{Seq: seq, IDHash: idHash, Kind: kind, CreatedAt: createdAt})
	idx.byTopic[topicID] = entries
}

// MaxSeq returns the highest Seq the topic holds, 0 when it holds nothing. It
// spans both kinds, because one kind's next slot must not land on the other's.
func (idx *L4Index) MaxSeq(topicID uint64) uint64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	entries := idx.byTopic[topicID]
	if len(entries) == 0 {
		return 0
	}
	return entries[len(entries)-1].Seq
}

// IDs returns one kind's record ids in Seq order; nil for a topic with nothing
// of that kind, which is an empty read, not an error.
func (idx *L4Index) IDs(topicID uint64, kind core.ArchiveKind) []uint64 {
	return idx.ids(topicID, &kind)
}

// AllIDs returns every id the topic holds, both kinds, in Seq order.
func (idx *L4Index) AllIDs(topicID uint64) []uint64 {
	return idx.ids(topicID, nil)
}

func (idx *L4Index) ids(topicID uint64, kind *core.ArchiveKind) []uint64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	entries := idx.byTopic[topicID]
	out := make([]uint64, 0, len(entries))
	for _, e := range entries {
		if kind != nil && e.Kind != *kind {
			continue
		}
		out = append(out, e.IDHash)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ExpiredBefore reports the ids created strictly before cutoff (Unix ms),
// grouped by topic. It reads the mirror and does not touch it: the caller
// tombstones the records first and only then calls RemoveIDs, so a failed delete
// never leaves the index naming a record that is still live on disk.
func (idx *L4Index) ExpiredBefore(cutoff int64) map[uint64][]uint64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make(map[uint64][]uint64)
	for topicID, entries := range idx.byTopic {
		for _, e := range entries {
			if e.CreatedAt < cutoff {
				out[topicID] = append(out[topicID], e.IDHash)
			}
		}
	}
	return out
}

// RemoveIDs drops specific records of one topic — the counterpart of deleting
// them — and returns how many went away. Leaving an entry that names a deleted
// record makes every later read of that topic fail on a record that is gone.
func (idx *L4Index) RemoveIDs(topicID uint64, idHashes []uint64) int {
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
	kept := slices.DeleteFunc(slices.Clone(entries), func(e l4Entry) bool {
		_, ok := doomed[e.IDHash]
		return ok
	})
	if len(kept) == len(entries) {
		return 0
	}
	if len(kept) == 0 {
		delete(idx.byTopic, topicID)
		return len(entries)
	}
	idx.byTopic[topicID] = kept
	return len(entries) - len(kept)
}

// RemoveTopic drops a whole topic's entries, the counterpart of deleting all the
// records it owned.
func (idx *L4Index) RemoveTopic(topicID uint64) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.byTopic, topicID)
}
