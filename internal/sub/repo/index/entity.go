// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"strings"

	"github.com/qyiun666/MemHop/internal/sub/common"
)

// entityEntry stores the L3 node hash and associated L2 IDs for an entity.
type entityEntry struct {
	nodeHash uint64
	l2IDs    []uint64
}

// FuzzyResult represents a fuzzy match result from BK-Tree search.
type FuzzyResult struct {
	Name     string
	NodeHash uint64
	Distance int
	L2IDs    []uint64
}

// EntityIndex provides exact and fuzzy entity matching using a BK-Tree.
type EntityIndex struct {
	entities map[string]entityEntry // lowercase name → entry
	bkTree   *common.BKTree
}

// NewEntityIndex creates an empty EntityIndex.
func NewEntityIndex() *EntityIndex {
	return &EntityIndex{
		entities: make(map[string]entityEntry),
		bkTree:   common.NewBKTree(),
	}
}

// AddEntity registers an entity with its L3 node hash and L2 IDs.
func (ei *EntityIndex) AddEntity(name string, nodeHash uint64, l2IDs []uint64) {
	key := strings.ToLower(name)
	ei.entities[key] = entityEntry{nodeHash: nodeHash, l2IDs: l2IDs}
	ei.bkTree.Insert(key)
}

// ExactMatch returns the node hash and L2 IDs for an exact match.
func (ei *EntityIndex) ExactMatch(term string) (uint64, []uint64, bool) {
	entry, ok := ei.entities[strings.ToLower(term)]
	if !ok {
		return 0, nil, false
	}
	return entry.nodeHash, entry.l2IDs, true
}

// FuzzyMatch searches the BK-Tree for entities within maxDist edit distance.
func (ei *EntityIndex) FuzzyMatch(term string, maxDist int) []FuzzyResult {
	var results []FuzzyResult
	for _, match := range ei.bkTree.Search(term, maxDist) {
		if entry, ok := ei.entities[match.Word]; ok {
			results = append(results, FuzzyResult{
				Name:     match.Word,
				NodeHash: entry.nodeHash,
				Distance: match.Dist,
				L2IDs:    entry.l2IDs,
			})
		}
	}
	return results
}

// IsEmpty returns true if no entities are registered.
func (ei *EntityIndex) IsEmpty() bool {
	return len(ei.entities) == 0
}
