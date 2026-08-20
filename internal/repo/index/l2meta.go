// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"slices"
	"strings"
	"sync"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

type L2Meta struct {
	IDHash       uint64
	PageRef      uint64
	Title        string
	Depth        uint8
	SceneID      uint64
	ParentID     *uint64
	ChildrenIDs  []uint64
	VectorOffset uint64
	TurnCount    uint32
	ArchiveCount int
	L3Refs       []uint64
	L4Refs       []uint64
	Timestamp    uint64

	// Full topic fields cached so ListTopicsL2 modes 1/2 can rebuild
	// TopicSlot values without reading topic records.
	UserKeywords   []string
	UserTimestamp  int64
	AgentKeywords  []string
	AgentTimestamp int64
	FusedKeywords  []string
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

// topicSlotJSON mirrors core.TopicSlot with every field plus the legacy
// created_at/updated_at keys that core.TopicSlot does not model, keeping
// the original Timestamp semantics in engine scans.
type topicSlotJSON struct {
	ID              uint64   `json:"id"`
	SceneID         uint64   `json:"scene_id"`
	ParentID        *uint64  `json:"parent_id"`
	ChildrenIDs     []uint64 `json:"children_ids"`
	Depth           uint8    `json:"depth"`
	UserKeywords    []string `json:"user_keywords"`
	UserTimestamp   int64    `json:"user_timestamp"`
	L4Refs          []uint64 `json:"l4_refs"`
	L3Refs          []uint64 `json:"l3_refs"`
	AgentKeywords   []string `json:"agent_keywords"`
	AgentTimestamp  int64    `json:"agent_timestamp"`
	FusedKeywords   []string `json:"fused_keywords"`
	CentroidPageRef uint64   `json:"centroid_page_ref"`
	CreatedAt       int64    `json:"created_at"`
	UpdatedAt       int64    `json:"updated_at"`
}

func topicToL2Meta(idHash uint64, t *topicSlotJSON) *L2Meta {
	src := t.FusedKeywords
	if len(src) == 0 {
		src = t.UserKeywords
	}
	title := strings.Join(src, ", ")
	l3Refs := t.L3Refs
	archiveCount := len(t.L4Refs)
	ts := max(max(t.UpdatedAt, t.CreatedAt), 0)
	return &L2Meta{
		IDHash:         idHash,
		Title:          title,
		Depth:          t.Depth,
		SceneID:        t.SceneID,
		ParentID:       t.ParentID,
		ChildrenIDs:    t.ChildrenIDs,
		VectorOffset:   t.CentroidPageRef,
		TurnCount:      uint32(len(t.ChildrenIDs)),
		ArchiveCount:   archiveCount,
		L3Refs:         l3Refs,
		L4Refs:         t.L4Refs,
		Timestamp:      uint64(ts),
		UserKeywords:   t.UserKeywords,
		UserTimestamp:  t.UserTimestamp,
		AgentKeywords:  t.AgentKeywords,
		AgentTimestamp: t.AgentTimestamp,
		FusedKeywords:  t.FusedKeywords,
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

func (idx *L2MetaIndex) GetL2IDsByL3(l3ID uint64) []uint64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	var ids []uint64
	for _, meta := range idx.entries {
		if slices.Contains(meta.L3Refs, l3ID) {
			ids = append(ids, meta.IDHash)
		}
	}
	return ids
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

func L2MetaFromTopic(t *core.TopicSlot) *L2Meta {
	src := t.FusedKeywords
	if len(src) == 0 {
		src = t.UserKeywords
	}
	return &L2Meta{
		IDHash:         t.ID,
		Title:          strings.Join(src, ", "),
		Depth:          t.Depth,
		SceneID:        t.SceneID,
		ParentID:       t.ParentID,
		ChildrenIDs:    t.ChildrenIDs,
		VectorOffset:   t.CentroidPageRef,
		TurnCount:      uint32(len(t.ChildrenIDs)),
		ArchiveCount:   len(t.L4Refs),
		L3Refs:         t.L3Refs,
		L4Refs:         t.L4Refs,
		UserKeywords:   t.UserKeywords,
		UserTimestamp:  t.UserTimestamp,
		AgentKeywords:  t.AgentKeywords,
		AgentTimestamp: t.AgentTimestamp,
		FusedKeywords:  t.FusedKeywords,
	}
}

// ToTopicSlot rebuilds the full topic slot from cached metadata. The field
// mapping matches core.TopicSlot exactly, so candidates returned from the
// cache are identical to freshly unmarshalled records.
func (m *L2Meta) ToTopicSlot() core.TopicSlot {
	return core.TopicSlot{
		ID:              m.IDHash,
		SceneID:         m.SceneID,
		ParentID:        m.ParentID,
		ChildrenIDs:     m.ChildrenIDs,
		Depth:           m.Depth,
		UserKeywords:    m.UserKeywords,
		UserTimestamp:   m.UserTimestamp,
		L4Refs:          m.L4Refs,
		L3Refs:          m.L3Refs,
		AgentKeywords:   m.AgentKeywords,
		AgentTimestamp:  m.AgentTimestamp,
		FusedKeywords:   m.FusedKeywords,
		CentroidPageRef: m.VectorOffset,
	}
}
