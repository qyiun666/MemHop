// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package importx

import (
	"errors"
	"path/filepath"
	"testing"

	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
	"memhop/internal/core/index"
	"memhop/internal/core/model"
	"memhop/internal/core/storage"
	"memhop/internal/query/write"
)

// --- Auxiliary function tests ---

func TestStringOr(t *testing.T) {
	tests := []struct {
		name string
		s    *string
		def  string
		want string
	}{
		{"nil returns default", nil, "default", "default"},
		{"non-nil returns value", strPtr("custom"), "default", "custom"},
		{"empty string pointer", strPtr(""), "default", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringOr(tt.s, tt.def)
			if got != tt.want {
				t.Errorf("stringOr(%v, %q) = %q; want %q", tt.s, tt.def, got, tt.want)
			}
		})
	}
}

func TestFirstOrNil(t *testing.T) {
	tests := []struct {
		name string
		ss   []string
		want *string
	}{
		{"empty slice", []string{}, nil},
		{"nil slice", nil, nil},
		{"single element", []string{"first"}, strPtr("first")},
		{"multiple elements", []string{"a", "b", "c"}, strPtr("a")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstOrNil(tt.ss)
			if tt.want == nil {
				if got != nil {
					t.Errorf("expected nil, got %v", *got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil result")
			}
			if *got != *tt.want {
				t.Errorf("firstOrNil = %q; want %q", *got, *tt.want)
			}
		})
	}
}

func TestMatchesKeyword(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		keyword string
		want    bool
	}{
		{"exact match", "Hello World", "Hello World", true},
		{"case insensitive", "Hello World", "hello world", true},
		{"mixed case", "HELLO World", "hello", true},
		{"keyword not found", "Hello World", "xyz", false},
		{"empty keyword", "Hello", "", true},
		{"empty text", "", "keyword", false},
		{"both empty", "", "", true},
		{"match substring", "programming", "gram", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesKeyword(tt.text, tt.keyword)
			if got != tt.want {
				t.Errorf("matchesKeyword(%q, %q) = %v; want %v", tt.text, tt.keyword, got, tt.want)
			}
		})
	}
}

// --- ImportMemory input validation tests ---

func TestImportMemory_UnknownLayer(t *testing.T) {
	eng, sparse, l3Idx, l3Deg, l3Cac := setupDeps(t)
	defer closeEngine(t, eng)

	_, err := ImportMemory(eng, sparse, l3Idx, l3Deg, l3Cac, ImportRequest{
		TargetLayer: "InvalidLayer",
	})
	if err == nil {
		t.Fatal("expected error for unknown target layer")
	}
	if !errors.Is(err, mherrors.ErrInvalidQuery) {
		t.Errorf("error should wrap ErrInvalidQuery")
	}
}

func TestImportMemory_L0Profile_NilProfileData(t *testing.T) {
	eng, sparse, l3Idx, l3Deg, l3Cac := setupDeps(t)
	defer closeEngine(t, eng)

	_, err := ImportMemory(eng, sparse, l3Idx, l3Deg, l3Cac, ImportRequest{
		TargetLayer: write.TargetProfile,
		Data:        ImportData{Profile: nil},
		Mode:        write.ImportOverwrite,
	})
	if err == nil {
		t.Fatal("expected error for nil profile data")
	}
}

func TestImportMemory_L0Profile_CreateNew(t *testing.T) {
	eng, sparse, l3Idx, l3Deg, l3Cac := setupDeps(t)
	defer closeEngine(t, eng)

	result, err := ImportMemory(eng, sparse, l3Idx, l3Deg, l3Cac, ImportRequest{
		TargetLayer: write.TargetProfile,
		Data: ImportData{
			Profile: &ProfileImportData{
				Name: strPtr("TestAgent"),
				Role: strPtr("Helper"),
			},
		},
		Mode: write.ImportOverwrite,
	})
	if err != nil {
		t.Fatalf("ImportMemory failed: %v", err)
	}
	if result.Status != write.ImportSuccess {
		t.Errorf("Status = %q; want %q", result.Status, write.ImportSuccess)
	}
	if len(result.CreatedIDs) != 1 {
		t.Errorf("CreatedIDs = %d; want 1", len(result.CreatedIDs))
	}
	if result.NodeCount != 1 {
		t.Errorf("NodeCount = %d; want 1", result.NodeCount)
	}
}

func TestImportMemory_L0Profile_SkipExisting(t *testing.T) {
	eng, sparse, l3Idx, l3Deg, l3Cac := setupDeps(t)
	defer closeEngine(t, eng)

	// First create a profile
	_, err := ImportMemory(eng, sparse, l3Idx, l3Deg, l3Cac, ImportRequest{
		TargetLayer: write.TargetProfile,
		Data: ImportData{
			Profile: &ProfileImportData{Name: strPtr("Existing")},
		},
		Mode: write.ImportOverwrite,
	})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}

	// Import with Skip mode should succeed and report skip
	result, err := ImportMemory(eng, sparse, l3Idx, l3Deg, l3Cac, ImportRequest{
		TargetLayer: write.TargetProfile,
		Data: ImportData{
			Profile: &ProfileImportData{Name: strPtr("Skipped")},
		},
		Mode: write.ImportSkip,
	})
	if err != nil {
		t.Fatalf("skip import: %v", err)
	}
	if result.Status != write.ImportSuccess {
		t.Errorf("Status = %q; want %q", result.Status, write.ImportSuccess)
	}
	if result.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d; want 1", result.SkippedCount)
	}
}

func TestImportMemory_L2Topics_EmptyTopics(t *testing.T) {
	eng, sparse, l3Idx, l3Deg, l3Cac := setupDeps(t)
	defer closeEngine(t, eng)

	_, err := ImportMemory(eng, sparse, l3Idx, l3Deg, l3Cac, ImportRequest{
		TargetLayer: write.TargetTopic,
		Data:        ImportData{Topics: []TopicImportItem{}},
		Mode:        write.ImportOverwrite,
	})
	if err == nil {
		t.Fatal("expected error for empty topics")
	}
	if !errors.Is(err, mherrors.ErrInvalidQuery) {
		t.Errorf("error should wrap ErrInvalidQuery")
	}
}

func TestImportMemory_L2Topics_CreateNew(t *testing.T) {
	eng, sparse, l3Idx, l3Deg, l3Cac := setupDeps(t)
	defer closeEngine(t, eng)

	result, err := ImportMemory(eng, sparse, l3Idx, l3Deg, l3Cac, ImportRequest{
		TargetLayer: write.TargetTopic,
		Data: ImportData{
			Topics: []TopicImportItem{
				{Title: "Test Topic", Summary: strPtr("A test topic"), Keywords: []string{"test"}},
			},
		},
		Mode: write.ImportOverwrite,
	})
	if err != nil {
		t.Fatalf("import topics: %v", err)
	}
	if result.Status != write.ImportSuccess {
		t.Errorf("Status = %q; want %q", result.Status, write.ImportSuccess)
	}
	if len(result.CreatedIDs) != 1 {
		t.Errorf("CreatedIDs = %d; want 1", len(result.CreatedIDs))
	}
}

func TestImportMemory_L3Knowledge_EmptyKnowledge(t *testing.T) {
	eng, sparse, l3Idx, l3Deg, l3Cac := setupDeps(t)
	defer closeEngine(t, eng)

	_, err := ImportMemory(eng, sparse, l3Idx, l3Deg, l3Cac, ImportRequest{
		TargetLayer: write.TargetKnowledge,
		Data:        ImportData{Knowledge: []KnowledgeImportItem{}},
		Mode:        write.ImportOverwrite,
	})
	if err == nil {
		t.Fatal("expected error for empty knowledge")
	}
	if !errors.Is(err, mherrors.ErrInvalidQuery) {
		t.Errorf("error should wrap ErrInvalidQuery")
	}
}

func TestImportMemory_L3Knowledge_CreateNew(t *testing.T) {
	eng, sparse, l3Idx, l3Deg, l3Cac := setupDeps(t)
	defer closeEngine(t, eng)

	result, err := ImportMemory(eng, sparse, l3Idx, l3Deg, l3Cac, ImportRequest{
		TargetLayer: write.TargetKnowledge,
		Data: ImportData{
			Knowledge: []KnowledgeImportItem{
				{
					Title:         "Test Knowledge",
					Domain:        "test_domain",
					KnowledgeType: "fact",
					Text:          "This is a test knowledge item for testing purposes.",
					Keywords:      []string{"test"},
				},
			},
		},
		Mode: write.ImportOverwrite,
	})
	if err != nil {
		t.Fatalf("import knowledge: %v", err)
	}
	if result.Status != write.ImportSuccess {
		t.Errorf("Status = %q; want %q", result.Status, write.ImportSuccess)
	}
	if len(result.CreatedIDs) != 1 {
		t.Errorf("CreatedIDs = %d; want 1", len(result.CreatedIDs))
	}
}

func TestImportMemory_L3Knowledge_SkipExisting(t *testing.T) {
	eng, sparse, l3Idx, l3Deg, l3Cac := setupDeps(t)
	defer closeEngine(t, eng)

	item := KnowledgeImportItem{
		Title:         "Test Knowledge",
		Domain:        "test_domain",
		KnowledgeType: "fact",
		Text:          "This is a test knowledge item.",
		Keywords:      []string{"test"},
	}

	// Create first
	_, err := ImportMemory(eng, sparse, l3Idx, l3Deg, l3Cac, ImportRequest{
		TargetLayer: write.TargetKnowledge,
		Data:        ImportData{Knowledge: []KnowledgeImportItem{item}},
		Mode:        write.ImportOverwrite,
	})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}

	// Skip mode should skip existing
	result, err := ImportMemory(eng, sparse, l3Idx, l3Deg, l3Cac, ImportRequest{
		TargetLayer: write.TargetKnowledge,
		Data:        ImportData{Knowledge: []KnowledgeImportItem{item}},
		Mode:        write.ImportSkip,
	})
	if err != nil {
		t.Fatalf("skip import: %v", err)
	}
	if result.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d; want 1", result.SkippedCount)
	}
}

// --- L3 index update tests ---

func TestUpdateL3Indexes_NilIndexes(t *testing.T) {
	// Should not panic with nil indexes
	nodes := []*model.HypergraphNode{
		{IDHash: 1, GraphID: 10, Title: "Test"},
	}
	// This should not panic
	updateL3Indexes(nodes, nil, nil, nil)
}

func TestUpdateL3Indexes_WithIndexes(t *testing.T) {
	l3Idx := index.NewL3Index()
	l3Deg := index.NewDegreeTracker()
	l3Cac := index.NewAdjacencyCache(10)

	nodes := []*model.HypergraphNode{
		{IDHash: hash.HashID("node1"), GraphID: hash.HashID("graph1"), Title: "Node1", NodeType: "fact"},
		{IDHash: hash.HashID("node2"), GraphID: hash.HashID("graph1"), Title: "Node2", NodeType: "concept"},
	}

	updateL3Indexes(nodes, l3Idx, l3Deg, l3Cac)
	// No panic = success, and nodes should be indexed
}

func TestImportMemory_L2Topics_WithKnowledgeTitle(t *testing.T) {
	eng, sparse, l3Idx, l3Deg, l3Cac := setupDeps(t)
	defer closeEngine(t, eng)

	// First create a knowledge node so the title resolves to a valid L3 hash
	knowledgeTitle := "MyKnowledge"
	_, err := ImportMemory(eng, sparse, l3Idx, l3Deg, l3Cac, ImportRequest{
		TargetLayer: write.TargetKnowledge,
		Data: ImportData{
			Knowledge: []KnowledgeImportItem{
				{Title: knowledgeTitle, Domain: "domain", KnowledgeType: "fact", Text: "Some knowledge.", Keywords: []string{"know"}},
			},
		},
		Mode: write.ImportOverwrite,
	})
	if err != nil {
		t.Fatalf("create knowledge: %v", err)
	}

	// Now import a topic that references the knowledge title
	result, err := ImportMemory(eng, sparse, l3Idx, l3Deg, l3Cac, ImportRequest{
		TargetLayer:    write.TargetTopic,
		Data: ImportData{
			Topics: []TopicImportItem{
				{Title: "TopicWithRef", Summary: strPtr("refers to knowledge"), Keywords: []string{"ref"}},
			},
		},
		Mode:           write.ImportOverwrite,
		KnowledgeTitle: &knowledgeTitle,
	})
	if err != nil {
		t.Fatalf("import topic with knowledge title: %v", err)
	}
	if result.Status != write.ImportSuccess {
		t.Errorf("Status = %q; want %q", result.Status, write.ImportSuccess)
	}
}

// --- helpers ---

func strPtr(s string) *string { return &s }

func setupDeps(t *testing.T) (*storage.StorageEngine, *index.SparseIndex, *index.L3Index, *index.DegreeTracker, *index.AdjacencyCache) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.meh")
	eng, err := storage.Create(p, 768)
	if err != nil {
		t.Fatalf("Create engine: %v", err)
	}
	return eng, index.NewSparseIndex(), index.NewL3Index(), index.NewDegreeTracker(), index.NewAdjacencyCache(128)
}

func closeEngine(t *testing.T, eng *storage.StorageEngine) {
	t.Helper()
	if err := eng.CloseNoCheckpoint(); err != nil {
		t.Errorf("CloseNoCheckpoint: %v", err)
	}
}
