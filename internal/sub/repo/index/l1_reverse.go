// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 reverse index for context lookup.

package index

import (
	"encoding/json"
	"slices"
	"sync"
)

// L1ReverseIndex maps L2 context_id → L1 ContextNode(s) pointing to it.
// Avoids O(N) btree scan for associated context lookups.
type L1ReverseIndex struct {
	mu    sync.RWMutex
	index map[uint64][]uint64 // context_id → [node_id_hash, ...]
}

// NewL1ReverseIndex creates an empty reverse index.
func NewL1ReverseIndex() *L1ReverseIndex {
	return &L1ReverseIndex{index: make(map[uint64][]uint64)}
}

// Add registers a node for a context_id (deduplicates).
func (r *L1ReverseIndex) Add(contextID, nodeIDHash uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	nodes := r.index[contextID]
	for _, nid := range nodes {
		if nid == nodeIDHash {
			return
		}
	}
	r.index[contextID] = append(nodes, nodeIDHash)
}

// RemoveNode removes a specific node from all contexts.
func (r *L1ReverseIndex) RemoveNode(nodeIDHash uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for ctxID, nodes := range r.index {
		filtered := slices.DeleteFunc(slices.Clone(nodes), func(x uint64) bool { return x == nodeIDHash })
		if len(filtered) == 0 {
			delete(r.index, ctxID)
		} else if len(filtered) != len(nodes) {
			r.index[ctxID] = filtered
		}
	}
}

// FindAssociated returns deduplicated L1 node hashes for given context IDs.
func (r *L1ReverseIndex) FindAssociated(contextIDs map[uint64]struct{}) []uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[uint64]struct{})
	var result []uint64
	for ctxID := range contextIDs {
		for _, nodeID := range r.index[ctxID] {
			if _, ok := seen[nodeID]; !ok {
				seen[nodeID] = struct{}{}
				result = append(result, nodeID)
			}
		}
	}
	return result
}

// Serialize encodes the reverse index to JSON bytes.
func (r *L1ReverseIndex) Serialize() ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return json.Marshal(r.index)
}

// DeserializeL1ReverseIndex restores from JSON bytes.
func DeserializeL1ReverseIndex(data []byte) (*L1ReverseIndex, error) {
	idx := NewL1ReverseIndex()
	if err := json.Unmarshal(data, &idx.index); err != nil {
		return nil, err
	}
	return idx, nil
}

// BuildL1ReverseIndex is defined in rebuild.go (shared single-pass scan).
