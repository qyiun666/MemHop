// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

func TestEntityIndex(t *testing.T) {
	t.Run("exact_match", func(t *testing.T) {
		ei := NewEntityIndex()
		ei.AddEntity("Rust Programming", 101, []uint64{1001, 1002})
		nodeHash, l2IDs, ok := ei.ExactMatch("rust programming")
		if !ok {
			t.Fatal("exact match should succeed")
		}
		if nodeHash != 101 {
			t.Errorf("expected nodeHash 101, got %d", nodeHash)
		}
		if len(l2IDs) != 2 {
			t.Errorf("expected 2 l2IDs, got %d", len(l2IDs))
		}
		// Case insensitive
		_, _, ok = ei.ExactMatch("RUST PROGRAMMING")
		if !ok {
			t.Error("case insensitive exact match should succeed")
		}
	})

	t.Run("fuzzy_match", func(t *testing.T) {
		ei := NewEntityIndex()
		ei.AddEntity("memhop", 1, []uint64{10})
		results := ei.FuzzyMatch("memhope", 2)
		found := false
		for _, r := range results {
			if r.Name == "memhop" {
				found = true
			}
		}
		if !found {
			t.Errorf("fuzzy match should find 'memhop', got %v", results)
		}
	})
}

func TestLevenshteinDistance(t *testing.T) {
	if d := common.LevenshteinDistance("kitten", "sitting"); d != 3 {
		t.Errorf("kitten→sitting should be 3, got %d", d)
	}
	if d := common.LevenshteinDistance("abc", "abc"); d != 0 {
		t.Errorf("same string should be 0, got %d", d)
	}
	if d := common.LevenshteinDistance("", "abc"); d != 3 {
		t.Errorf("empty→abc should be 3, got %d", d)
	}
}
