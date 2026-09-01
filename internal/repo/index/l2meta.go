// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"iter"
	"slices"
	"sync"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

// L2Meta is the in-memory cache of one topic record: exactly the fields
// ToTopicSlot needs to rebuild the slot without reading the record. Derived
// display fields (title, turn count, vector offset) retired with the
// retrieval subsystem that read them.
type L2Meta struct {
	IDHash         uint64
	Depth          uint8
	SceneID        uint64
	ParentID       *uint64
	ChildrenIDs    []uint64
	FusedKeywords  []string
	UserTimestamp  int64
	AgentTimestamp int64
	L4Refs         []uint64
}

type L2MetaIndex struct {
	mu      sync.RWMutex
	entries map[uint64]*L2Meta
	byScene map[uint64][]uint64
}

func NewL2MetaIndex() *L2MetaIndex {
	return &L2MetaIndex{
		entries: make(map[uint64]*L2Meta),
		byScene: make(map[uint64][]uint64),
	}
}

// BuildL2MetaFromEngine is defined in rebuild.go (shared single-pass scan).

// L2MetaFromTopic is the single conversion point from a stored topic record
// to its cached metadata.
func L2MetaFromTopic(t *core.TopicSlot) *L2Meta {
	return &L2Meta{
		IDHash:         t.ID,
		Depth:          t.Depth,
		SceneID:        t.SceneID,
		ParentID:       t.ParentID,
		ChildrenIDs:    t.ChildrenIDs,
		FusedKeywords:  t.FusedKeywords,
		UserTimestamp:  t.UserTimestamp,
		AgentTimestamp: t.AgentTimestamp,
		L4Refs:         t.L4Refs,
	}
}

func (idx *L2MetaIndex) Get(idHash uint64) *L2Meta {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.entries[idHash]
}

func (idx *L2MetaIndex) Update(meta *L2Meta) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if old, exists := idx.entries[meta.IDHash]; exists {
		idx.removeFromIndices(old.SceneID, meta.IDHash)
	}
	idx.insertMeta(meta)
}

func (idx *L2MetaIndex) Remove(idHash uint64) *L2Meta {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	meta, ok := idx.entries[idHash]
	if !ok {
		return nil
	}
	idx.removeFromIndices(meta.SceneID, idHash)
	delete(idx.entries, idHash)
	return meta
}

func (idx *L2MetaIndex) GetByScene(sceneID uint64) []uint64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	// Clone so callers never hold or mutate the internal slice; the
	// byScene list may be rebuilt on the next Remove/Update.
	return slices.Clone(idx.byScene[sceneID])
}

func (idx *L2MetaIndex) Len() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.entries)
}

func (idx *L2MetaIndex) IsEmpty() bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.entries) == 0
}

// Iter iterates over all entries as a pull iterator; the read lock is held
// for the whole loop, so yields must not call back into the index.
func (idx *L2MetaIndex) Iter() iter.Seq2[uint64, *L2Meta] {
	return func(yield func(uint64, *L2Meta) bool) {
		idx.mu.RLock()
		defer idx.mu.RUnlock()
		for id, meta := range idx.entries {
			if !yield(id, meta) {
				return
			}
		}
	}
}

func (idx *L2MetaIndex) insertMeta(meta *L2Meta) {
	idx.entries[meta.IDHash] = meta
	idx.byScene[meta.SceneID] = append(idx.byScene[meta.SceneID], meta.IDHash)
}

func (idx *L2MetaIndex) removeFromIndices(sceneID uint64, idHash uint64) {
	if ids, ok := idx.byScene[sceneID]; ok {
		filtered := slices.DeleteFunc(slices.Clone(ids), func(x uint64) bool { return x == idHash })
		if len(filtered) == 0 {
			delete(idx.byScene, sceneID)
		} else {
			idx.byScene[sceneID] = filtered
		}
	}
}

// ToTopicSlot rebuilds the full topic slot from cached metadata. The field
// mapping matches core.TopicSlot exactly, so candidates returned from the
// cache are identical to freshly unmarshalled records.
func (m *L2Meta) ToTopicSlot() core.TopicSlot {
	return core.TopicSlot{
		ID:             m.IDHash,
		SceneID:        m.SceneID,
		ParentID:       m.ParentID,
		ChildrenIDs:    m.ChildrenIDs,
		Depth:          m.Depth,
		FusedKeywords:  m.FusedKeywords,
		UserTimestamp:  m.UserTimestamp,
		AgentTimestamp: m.AgentTimestamp,
		L4Refs:         m.L4Refs,
	}
}
