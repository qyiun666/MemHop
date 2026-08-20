// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package common

// BKTree is a BK-Tree for fuzzy string matching based on edit distance.
type BKTree struct {
	nodes []bkNode
}

type BKMatch struct {
	Word string
	Dist int
}

type bkNode struct {
	word     string
	children map[int]int // edit_distance → node index
}

func NewBKTree() *BKTree {
	return &BKTree{}
}

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

func (t *BKTree) Search(word string, maxDist int) []BKMatch {
	if len(t.nodes) == 0 {
		return nil
	}
	var prev, curr []int // Levenshtein work buffers, reused across the whole search
	var results []BKMatch
	t.searchRecursive(0, word, maxDist, &results, &prev, &curr)
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

func (t *BKTree) searchRecursive(nodeIdx int, word string, maxDist int, results *[]BKMatch, prev, curr *[]int) {
	node := &t.nodes[nodeIdx]
	dist, p, c := LevenshteinDistanceInto(node.word, word, *prev, *curr)
	*prev, *curr = p, c
	if dist <= maxDist {
		*results = append(*results, BKMatch{Word: node.word, Dist: dist})
	}
	minDist := max(dist-maxDist, 0)
	maxDistRange := dist + maxDist
	for childDist, childIdx := range node.children {
		if childDist >= minDist && childDist <= maxDistRange {
			t.searchRecursive(childIdx, word, maxDist, results, prev, curr)
		}
	}
}
