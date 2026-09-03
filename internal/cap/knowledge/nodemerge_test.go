// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package knowledge

import (
	"slices"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

// MergeFields is the append-side import policy: a blank import never erases
// what is stored, content only grows when it adds information, keywords are
// unioned and a blank source reference keeps the positional link.
func TestMergeFieldsKeepsAndGrows(t *testing.T) {
	ref := "page:7"
	node := &core.HypergraphNode{
		Title: "keep me", NodeType: "concept", Content: "rust owns memory",
		Keywords: []string{"rust"}, SourceRef: &ref, UpdatedAt: 1,
	}

	MergeFields(node, "", "", nil, "", 2)
	if node.NodeType != "concept" || node.Content != "rust owns memory" ||
		node.SourceRef == nil || *node.SourceRef != "page:7" {
		t.Fatalf("blank import changed stored fields: %+v", node)
	}
	if node.UpdatedAt != 2 {
		t.Fatalf("UpdatedAt = %d, want the import timestamp", node.UpdatedAt)
	}

	// A substring of what is already there adds nothing; a disjoint fact does.
	MergeFields(node, "fact", "owns memory", []string{"memory"}, "page:9", 3)
	if node.NodeType != "fact" {
		t.Fatalf("non-empty node type must replace: %+v", node)
	}
	if node.Content != "rust owns memory" {
		t.Fatalf("contained content appended anyway: %q", node.Content)
	}
	MergeFields(node, "", "borrowing is compile-time", []string{"rust", "borrow"}, "", 4)
	if node.Content != "rust owns memory\nborrowing is compile-time" {
		t.Fatalf("disjoint content not appended: %q", node.Content)
	}
	if !slices.Equal(node.Keywords, []string{"rust", "memory", "borrow"}) {
		t.Fatalf("keywords = %v, want the first-seen-order union", node.Keywords)
	}
	if node.SourceRef == nil || *node.SourceRef != "page:9" {
		t.Fatalf("blank source ref must keep the current one: %+v", node.SourceRef)
	}
}

// OverwriteFields is the replace-side policy: every mutable field follows the
// import, an empty source reference clears it, and identity stays untouched.
func TestOverwriteFieldsReplacesMutableFields(t *testing.T) {
	ref := "page:7"
	node := &core.HypergraphNode{
		IDHash: 42, GraphID: 7, Title: "keep me",
		NodeType: "concept", Content: "old", Keywords: []string{"old"},
		SourceRef: &ref, CreatedAt: 1, UpdatedAt: 1,
	}

	OverwriteFields(node, "fact", "new", []string{"fresh"}, "", 5)

	if node.IDHash != 42 || node.GraphID != 7 || node.Title != "keep me" || node.CreatedAt != 1 {
		t.Fatalf("identity or creation time touched: %+v", node)
	}
	if node.NodeType != "fact" || node.Content != "new" || len(node.Keywords) != 1 || node.Keywords[0] != "fresh" {
		t.Fatalf("mutable fields not replaced: %+v", node)
	}
	if node.SourceRef != nil {
		t.Fatalf("empty source ref must clear the link, got %q", *node.SourceRef)
	}
	if node.UpdatedAt != 5 {
		t.Fatalf("UpdatedAt = %d, want the import timestamp", node.UpdatedAt)
	}
}
