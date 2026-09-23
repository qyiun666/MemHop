// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4Index caches one agent domain's content inventory grouped by topic, so a
// read can enumerate what a turn holds. A single record is addressable as
// (topic, Seq) without any index — that is what its id hashes — but listing a
// topic's Seqs is exactly what this cache is for, and it is the only list of
// what a topic owns.
//
// Entries are ordered by Seq ascending. Seq is one space per topic shared by
// both kinds and a total order: the dialogue's two slots are 1 and 2 by
// convention, anything allocated lands above what is held. So Seq alone gives
// a transcript its question-first reading and an event log its chronology.
//
// Kind is carried by every entry and takes part in no ordering: the two kinds
// come out of one Seq-ordered list as disjoint sets, so an entry that did not
// say which kind it is would make a read for one kind answer with the other.
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
	// NodeSeq is the plan step an event names, 0 for a record bound to none. The
	// plan's ordinal allocator reads it: the address (topic, ordinal) is shared by
	// the step record and the events that name it, so a step may only be re-issued
	// at an ordinal no surviving event still points at.
	NodeSeq uint32
}

type L4Index struct {
	mu      sync.RWMutex
	byTopic map[uint64][]l4Entry // Seq ascending; one entry per (topic, Seq)
}

func NewL4Index() *L4Index {
	return &L4Index{byTopic: make(map[uint64][]l4Entry)}
}

// BuildL4FromEngine scans one agent domain's L4 records into a fresh index,
// dropping whatever will not decode: the frames walked already passed CRC at
// open, so this is a payload that does not fit its shape, and refusing it would
// make the whole domain unopenable. What a drop costs is a Seq MaxSeq no longer
// sees — the next append gets the dropped slot's Seq and overwrites it. Each
// drop is logged by the scan itself.
func BuildL4FromEngine(engine *core.StorageEngine, agentID uint64) *L4Index {
	idx := NewL4Index()
	for _, arc := range core.CollectAllArchives(engine, agentID) {
		idx.Append(arc.TopicID, arc.Seq, arc.IDHash, arc.Kind, arc.CreatedAt, arc.NodeSeq)
	}
	return idx
}

// Append records one content slot, replacing whatever held that Seq: re-writing
// a Seq is an in-place overwrite on the disk too, so the mirror must not grow.
// An utterance can land after the events of its turn (a caller records events
// while it runs and settles the turn afterwards), so entries insert in Seq
// order rather than at the tail.
func (idx *L4Index) Append(topicID, seq, idHash uint64, kind core.ArchiveKind, createdAt int64, nodeSeq uint32) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	entries := idx.byTopic[topicID]
	at, _ := slices.BinarySearchFunc(entries, seq, func(e l4Entry, s uint64) int {
		return cmp.Compare(e.Seq, s)
	})
	e := l4Entry{Seq: seq, IDHash: idHash, Kind: kind, CreatedAt: createdAt, NodeSeq: nodeSeq}
	if at < len(entries) && entries[at].Seq == seq {
		entries[at] = e
		return
	}
	entries = slices.Insert(entries, at, e)
	idx.byTopic[topicID] = entries
}

// MaxNodeSeq returns the highest plan ordinal the topic's records name, 0 when none
// does. Events age on their own clock and can outlive the step they bind to, so this
// is how large the allocator must skip: an ordinal below it is still spoken of.
func (idx *L4Index) MaxNodeSeq(topicID uint64) uint32 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	var top uint32
	for _, e := range idx.byTopic[topicID] {
		if e.NodeSeq > top {
			top = e.NodeSeq
		}
	}
	return top
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
// them. Leaving an entry that names a deleted record makes every later read of that
// topic fail on a record that is gone.
func (idx *L4Index) RemoveIDs(topicID uint64, idHashes []uint64) {
	doomed := make(map[uint64]struct{}, len(idHashes))
	for _, h := range idHashes {
		doomed[h] = struct{}{}
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	entries := idx.byTopic[topicID]
	if len(entries) == 0 {
		return
	}
	kept := slices.DeleteFunc(slices.Clone(entries), func(e l4Entry) bool {
		_, ok := doomed[e.IDHash]
		return ok
	})
	if len(kept) == len(entries) {
		return
	}
	if len(kept) == 0 {
		delete(idx.byTopic, topicID)
		return
	}
	idx.byTopic[topicID] = kept
}

// RemoveTopic drops a whole topic's entries, the counterpart of deleting all the
// records it owned.
func (idx *L4Index) RemoveTopic(topicID uint64) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.byTopic, topicID)
}
