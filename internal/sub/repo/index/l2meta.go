// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"encoding/json"
	"slices"
	"sync"

	"github.com/qyiun666/MemHop/internal/common/strutil"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// L2Meta is lightweight metadata for an L2 TopicSlot.
type L2Meta struct {
	IDHash       uint64
	PageRef      uint64
	Title        string
	Depth        uint8
	SceneID      uint64
	ChildrenIDs  []uint64
	VectorOffset uint64
	TurnCount    uint32
	ArchiveCount int
	L3Refs       []uint64
	Timestamp    uint64
}

// L2MetaIndex is an in-memory index of L2 metadata.
type L2MetaIndex struct {
	mu      sync.RWMutex
	entries map[uint64]*L2Meta
	byScene map[uint64][]uint64
}

// NewL2MetaIndex creates an empty L2MetaIndex.
func NewL2MetaIndex() *L2MetaIndex {
	return &L2MetaIndex{
		entries: make(map[uint64]*L2Meta),
		byScene: make(map[uint64][]uint64),
	}
}

// BuildL2MetaFromEngine scans the storage engine for L2 TopicSlot records.
func BuildL2MetaFromEngine(engine *storage.StorageEngine) *L2MetaIndex {
	idx := NewL2MetaIndex()

	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil {
			return true
		}
		if rt != storage.RecL2Topic {
			return true
		}
		var topic topicSlotJSON
		if err := json.Unmarshal(data, &topic); err != nil {
			return true
		}
		meta := topicToL2Meta(idHash, &topic)
		idx.insertMeta(meta)
		return true
	})

	return idx
}

// topicSlotJSON is a minimal deserialization target for TopicSlot.
type topicSlotJSON struct {
	ID              uint64   `json:"id"`
	SceneID         uint64   `json:"scene_id"`
	ChildrenIDs     []uint64 `json:"children_ids"`
	Depth           uint8    `json:"depth"`
	UserKeywords    []string `json:"user_keywords"`
	AgentKeywords   []string `json:"agent_keywords"`
	FusedKeywords   []string `json:"fused_keywords"`
	CentroidPageRef uint64   `json:"centroid_page_ref"`
	L4Refs          uint64   `json:"l4_refs"`
	L3Refs          []uint64 `json:"l3_refs"`
	CreatedAt       int64    `json:"created_at"`
	UpdatedAt       int64    `json:"updated_at"`
}

func topicToL2Meta(idHash uint64, t *topicSlotJSON) *L2Meta {
	title := joinKeywords(t.FusedKeywords, t.UserKeywords)
	l3Refs := t.L3Refs
	archiveCount := 0
	if t.L4Refs != 0 {
		archiveCount = 1
	}
	ts := t.UpdatedAt
	if ts < t.CreatedAt {
		ts = t.CreatedAt
	}
	if ts < 0 {
		ts = 0
	}
	return &L2Meta{
		IDHash:       idHash,
		Title:        title,
		Depth:        t.Depth,
		SceneID:      t.SceneID,
		ChildrenIDs:  t.ChildrenIDs,
		VectorOffset: t.CentroidPageRef,
		TurnCount:    uint32(len(t.ChildrenIDs)),
		ArchiveCount: archiveCount,
		L3Refs:       l3Refs,
		Timestamp:    uint64(ts),
	}
}

func joinKeywords(primary, fallback []string) string {
	src := primary
	if len(src) == 0 {
		src = fallback
	}
	if len(src) == 0 {
		return ""
	}
	result := src[0]
	for _, s := range src[1:] {
		result += ", " + s
	}
	return result
}

// Get returns metadata for an L2 by idHash.
func (idx *L2MetaIndex) Get(idHash uint64) *L2Meta {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.entries[idHash]
}

// Update inserts or replaces an L2Meta entry.
func (idx *L2MetaIndex) Update(meta *L2Meta) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if old, exists := idx.entries[meta.IDHash]; exists {
		idx.removeFromIndices(old.SceneID, meta.IDHash)
	}
	idx.insertMeta(meta)
}

// Remove removes and returns an entry.
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

// GetByScene returns all L2 IDs belonging to a scene.
func (idx *L2MetaIndex) GetByScene(sceneID uint64) []uint64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.byScene[sceneID]
}

// GetL2IDsByL3 returns all L2 IDs whose l3_refs contain the given l3ID.
func (idx *L2MetaIndex) GetL2IDsByL3(l3ID uint64) []uint64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	var ids []uint64
	for _, meta := range idx.entries {
		for _, ref := range meta.L3Refs {
			if ref == l3ID {
				ids = append(ids, meta.IDHash)
				break
			}
		}
	}
	return ids
}

// Len returns the number of indexed L2 entries.
func (idx *L2MetaIndex) Len() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.entries)
}

// IsEmpty returns true if no entries are indexed.
func (idx *L2MetaIndex) IsEmpty() bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.entries) == 0
}

// Iter iterates over all entries. Return false from fn to stop.
func (idx *L2MetaIndex) Iter(fn func(idHash uint64, meta *L2Meta) bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for id, meta := range idx.entries {
		if !fn(id, meta) {
			return
		}
	}
}

// --- internal helpers ---

func (idx *L2MetaIndex) insertMeta(meta *L2Meta) {
	idx.entries[meta.IDHash] = meta
	idx.byScene[meta.SceneID] = append(idx.byScene[meta.SceneID], meta.IDHash)
}

func (idx *L2MetaIndex) removeFromIndices(sceneID uint64, idHash uint64) {
	if ids, ok := idx.byScene[sceneID]; ok {
		filtered := removeUint64(ids, idHash)
		if len(filtered) == 0 {
			delete(idx.byScene, sceneID)
		} else {
			idx.byScene[sceneID] = filtered
		}
	}
}

func removeUint64(slice []uint64, v uint64) []uint64 {
	return slices.DeleteFunc(slices.Clone(slice), func(x uint64) bool { return x == v })
}

// L2MetaFromTopic builds a lightweight L2Meta entry from a TopicSlot.
func L2MetaFromTopic(t *model.TopicSlot) *L2Meta {
	archiveCount := 0
	if t.L4Refs != 0 {
		archiveCount = 1
	}
	return &L2Meta{
		IDHash:       t.ID,
		Title:        strutil.JoinStrings(t.UserKeywords, ", "),
		Depth:        t.Depth,
		SceneID:      t.SceneID,
		ChildrenIDs:  t.ChildrenIDs,
		VectorOffset: t.CentroidPageRef,
		ArchiveCount: archiveCount,
		L3Refs:       t.L3Refs,
	}
}
