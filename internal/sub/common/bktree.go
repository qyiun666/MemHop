// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package common

// BKTree is a BK-Tree for fuzzy string matching based on edit distance.
type BKTree struct {
	nodes []bkNode
}

// BKMatch is one search hit with its edit distance.
type BKMatch struct {
	Word string
	Dist int
}

type bkNode struct {
	word     string
	children map[int]int // edit_distance → node index
}

// NewBKTree creates an empty BK-Tree.
func NewBKTree() *BKTree {
	return &BKTree{}
}

// Insert adds a word to the tree (duplicates are ignored).
func (t *BKTree) Insert(word string) {
	if len(t.nodes) == 0 {
		t.nodes = append(t.nodes, bkNode{word: word, children: make(map[int]int)})
		return
	}
	if t.contains(word) {
		return
	}
	t.insertRecursive(0, word)
}

// Search returns all words within maxDist edit distance of word.
func (t *BKTree) Search(word string, maxDist int) []BKMatch {
	if len(t.nodes) == 0 {
		return nil
	}
	var results []BKMatch
	t.searchRecursive(0, word, maxDist, &results)
	return results
}

func (t *BKTree) contains(word string) bool {
	return t.containsRecursive(0, word)
}

func (t *BKTree) containsRecursive(nodeIdx int, word string) bool {
	dist := LevenshteinDistance(t.nodes[nodeIdx].word, word)
	if dist == 0 {
		return true
	}
	if nextIdx, ok := t.nodes[nodeIdx].children[dist]; ok {
		return t.containsRecursive(nextIdx, word)
	}
	return false
}

func (t *BKTree) insertRecursive(nodeIdx int, word string) {
	dist := LevenshteinDistance(t.nodes[nodeIdx].word, word)
	if nextIdx, ok := t.nodes[nodeIdx].children[dist]; ok {
		t.insertRecursive(nextIdx, word)
		return
	}
	newIdx := len(t.nodes)
	t.nodes = append(t.nodes, bkNode{word: word, children: make(map[int]int)})
	t.nodes[nodeIdx].children[dist] = newIdx
}

func (t *BKTree) searchRecursive(nodeIdx int, word string, maxDist int, results *[]BKMatch) {
	node := &t.nodes[nodeIdx]
	dist := LevenshteinDistance(node.word, word)
	if dist <= maxDist {
		*results = append(*results, BKMatch{Word: node.word, Dist: dist})
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
