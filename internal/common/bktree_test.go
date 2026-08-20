// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package common

import "testing"

func TestBKTree(t *testing.T) {
	tree := NewBKTree()
	tree.Insert("apple")
	tree.Insert("apply")

	matches := tree.Search("apple", 0)
	if len(matches) != 1 {
		t.Errorf("exact search should find 1, got %d", len(matches))
	}

	matches = tree.Search("aple", 1)
	if len(matches) == 0 {
		t.Error("fuzzy search should find at least 1 match")
	}

	for range 100 {
		tree.Insert("apple")
	}
	if len(tree.nodes) != 2 { // apple + apply
		t.Errorf("duplicate insertions should not create new nodes, got %d", len(tree.nodes))
	}
}
