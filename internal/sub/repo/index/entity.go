// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import "strings"

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

// EntityMatch represents a recognized entity in text.
type EntityMatch struct {
	Name     string
	NodeHash uint64
	L2IDs    []uint64
	Start    int
	End      int
}

// EntityIndex provides exact and fuzzy entity matching using a BK-Tree.
type EntityIndex struct {
	entities map[string]entityEntry // lowercase name → entry
	bkTree   *bkTree
	nodeToL2 map[uint64][]uint64 // node_hash → l2_ids
}

// NewEntityIndex creates an empty EntityIndex.
func NewEntityIndex() *EntityIndex {
	return &EntityIndex{
		entities: make(map[string]entityEntry),
		bkTree:   newBkTree(),
		nodeToL2: make(map[uint64][]uint64),
	}
}

// AddEntity registers an entity with its L3 node hash and L2 IDs.
func (ei *EntityIndex) AddEntity(name string, nodeHash uint64, l2IDs []uint64) {
	key := strings.ToLower(name)
	entry := ei.nodeToL2[nodeHash]
	for _, id := range l2IDs {
		if !containsUint64(entry, id) {
			entry = append(entry, id)
		}
	}
	ei.nodeToL2[nodeHash] = entry
	ei.entities[key] = entityEntry{nodeHash: nodeHash, l2IDs: l2IDs}
	ei.bkTree.insert(key)
}

// AddLexicon adds words to the entity index without node associations.
func (ei *EntityIndex) AddLexicon(words []string) {
	for _, w := range words {
		key := strings.ToLower(w)
		if _, exists := ei.entities[key]; !exists {
			ei.entities[key] = entityEntry{}
			ei.bkTree.insert(key)
		}
	}
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
	for _, match := range ei.bkTree.search(term, maxDist) {
		if entry, ok := ei.entities[match.word]; ok {
			results = append(results, FuzzyResult{
				Name:     match.word,
				NodeHash: entry.nodeHash,
				Distance: match.dist,
				L2IDs:    entry.l2IDs,
			})
		}
	}
	return results
}

// RecognizeEntities finds entities in text using tokenization and fuzzy matching.
// Score: exact = 1.0, fuzzy = 1.0 / (1 + edit_distance).
func (ei *EntityIndex) RecognizeEntities(text string) []EntityMatch {
	words := TokenizeWords(text)
	tokens := make([]string, len(words))
	copy(tokens, words)
	// Add adjacent word pairs for multi-word entity names.
	for i := 0; i+1 < len(words); i++ {
		tokens = append(tokens, words[i]+" "+words[i+1])
	}

	bestScores := make(map[string]float32)
	for _, token := range tokens {
		for _, fr := range ei.FuzzyMatch(token, 2) {
			score := 1.0 / (1.0 + float32(fr.Distance))
			if score > bestScores[fr.Name] {
				bestScores[fr.Name] = score
			}
		}
	}

	var matches []EntityMatch
	for name, score := range bestScores {
		if entry, ok := ei.entities[name]; ok {
			matches = append(matches, EntityMatch{
				Name:     name,
				NodeHash: entry.nodeHash,
				L2IDs:    entry.l2IDs,
			})
			_ = score // used for ranking in caller
		}
	}
	return matches
}

// IsEmpty returns true if no entities are registered.
func (ei *EntityIndex) IsEmpty() bool {
	return len(ei.entities) == 0
}

// RebuildNodeToL2 rebuilds the node_hash → l2_ids reverse index.
func (ei *EntityIndex) RebuildNodeToL2() {
	ei.nodeToL2 = make(map[uint64][]uint64)
	for _, entry := range ei.entities {
		if entry.nodeHash == 0 {
			continue
		}
		existing := ei.nodeToL2[entry.nodeHash]
		for _, id := range entry.l2IDs {
			if !containsUint64(existing, id) {
				existing = append(existing, id)
			}
		}
		ei.nodeToL2[entry.nodeHash] = existing
	}
}

// L2IDsForNode returns L2 IDs associated with an L3 node hash.
func (ei *EntityIndex) L2IDsForNode(nodeHash uint64) []uint64 {
	return ei.nodeToL2[nodeHash]
}

func containsUint64(slice []uint64, v uint64) bool {
	for _, s := range slice {
		if s == v {
			return true
		}
	}
	return false
}

// ============================================================================
// BK-Tree for fuzzy matching based on edit distance
// ============================================================================

type bkNode struct {
	word     string
	children map[int]int // edit_distance → node index
}

type bkTree struct {
	nodes []bkNode
}

func newBkTree() *bkTree {
	return &bkTree{}
}

func (t *bkTree) insert(word string) {
	if len(t.nodes) == 0 {
		t.nodes = append(t.nodes, bkNode{word: word, children: make(map[int]int)})
		return
	}
	if t.contains(word) {
		return
	}
	t.insertRecursive(0, word)
}

func (t *bkTree) contains(word string) bool {
	if len(t.nodes) == 0 {
		return false
	}
	return t.containsRecursive(0, word)
}

func (t *bkTree) containsRecursive(nodeIdx int, word string) bool {
	dist := levenshteinDistance(t.nodes[nodeIdx].word, word)
	if dist == 0 {
		return true
	}
	if nextIdx, ok := t.nodes[nodeIdx].children[dist]; ok {
		return t.containsRecursive(nextIdx, word)
	}
	return false
}

func (t *bkTree) insertRecursive(nodeIdx int, word string) {
	dist := levenshteinDistance(t.nodes[nodeIdx].word, word)
	if nextIdx, ok := t.nodes[nodeIdx].children[dist]; ok {
		t.insertRecursive(nextIdx, word)
		return
	}
	newIdx := len(t.nodes)
	t.nodes = append(t.nodes, bkNode{word: word, children: make(map[int]int)})
	t.nodes[nodeIdx].children[dist] = newIdx
}

type bkMatch struct {
	word string
	dist int
}

func (t *bkTree) search(word string, maxDist int) []bkMatch {
	if len(t.nodes) == 0 {
		return nil
	}
	var results []bkMatch
	t.searchRecursive(0, word, maxDist, &results)
	return results
}

func (t *bkTree) searchRecursive(nodeIdx int, word string, maxDist int, results *[]bkMatch) {
	node := &t.nodes[nodeIdx]
	dist := levenshteinDistance(node.word, word)
	if dist <= maxDist {
		*results = append(*results, bkMatch{word: node.word, dist: dist})
	}
	minDist := dist - maxDist
	if minDist < 0 {
		minDist = 0
	}
	maxDistRange := dist + maxDist
	for childDist, childIdx := range node.children {
		if childDist >= minDist && childDist <= maxDistRange {
			t.searchRecursive(childIdx, word, maxDist, results)
		}
	}
}

// levenshteinDistance computes the edit distance between two strings.
func levenshteinDistance(a, b string) int {
	aRunes := []rune(a)
	bRunes := []rune(b)
	m, n := len(aRunes), len(bRunes)
	if m == 0 {
		return n
	}
	if n == 0 {
		return m
	}

	prev := make([]int, n+1)
	curr := make([]int, n+1)
	for j := 0; j <= n; j++ {
		prev[j] = j
	}
	for i := 1; i <= m; i++ {
		curr[0] = i
		for j := 1; j <= n; j++ {
			cost := 1
			if aRunes[i-1] == bRunes[j-1] {
				cost = 0
			}
			curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[n]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
