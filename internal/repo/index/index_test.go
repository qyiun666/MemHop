// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"math"
	"testing"
)

func TestBM25Score(t *testing.T) {
	idx := NewSparseIndex()
	idx.AddDocument(1, []string{"test", "term"}, 2)
	score := idx.BM25Score([]string{"test"}, 1)
	// IDF = ln((1 - 1 + 0.5) / (1 + 0.5) + 1.0) = ln(1.333) ≈ 0.288
	// TF norm = (1 * 2.2) / (1 + 1.2 * (1 - 0.75 + 0.75 * 2/2)) = 2.2 / 2.2 = 1.0
	// Score ≈ 0.288
	if score < 0.25 || score > 0.35 {
		t.Errorf("BM25 score should be ~0.288, got %f", score)
	}
}

func TestBM25IDFRareTerm(t *testing.T) {
	idx := NewSparseIndex()
	for i := range uint64(10) {
		idx.AddDocument(i, []string{"common"}, 1)
	}
	idx.AddDocument(100, []string{"rare"}, 1)
	idfRare := idx.idf(1)    // "rare" appears in 1 doc
	idfCommon := idx.idf(10) // "common" appears in 10 docs
	if idfRare <= idfCommon {
		t.Errorf("rare term IDF (%f) should be > common term IDF (%f)", idfRare, idfCommon)
	}
}

func TestSparseIndexSearch(t *testing.T) {
	idx := NewSparseIndex()
	idx.AddDocument(1, Tokenize("machine learning algorithms"), 3)
	idx.AddDocument(2, Tokenize("deep learning neural networks"), 4)
	idx.AddDocument(3, Tokenize("natural language processing"), 3)

	results := idx.Search([]string{"learning"}, 10)
	if len(results) < 2 {
		t.Errorf("search for 'learning' should find at least 2 docs, got %d", len(results))
	}

	// doc 1 has "machine" + "learning", searching "machine learning" should rank doc 1 first
	results2 := idx.Search([]string{"machine", "learning"}, 10)
	if len(results2) == 0 || results2[0].IDHash != 1 {
		t.Errorf("search for 'machine learning' should rank doc 1 first, got %v", results2)
	}
}

func TestSparseIndexTopK(t *testing.T) {
	idx := NewSparseIndex()
	for i := range uint64(5) {
		tokens := Tokenize("document number " + string(rune('a'+i)))
		idx.AddDocument(i, tokens, uint32(len(tokens)))
	}
	results := idx.Search([]string{"document"}, 3)
	if len(results) > 3 {
		t.Errorf("top-k=3 should return at most 3 results, got %d", len(results))
	}
}

func TestAddRemoveDocument(t *testing.T) {
	idx := NewSparseIndex()
	terms := Tokenize("machine learning is great")
	idx.AddDocument(1, terms, uint32(len(terms)))
	if idx.Len() != 1 {
		t.Errorf("expected len 1, got %d", idx.Len())
	}
	idx.RemoveDocument(1)
	if !idx.IsEmpty() {
		t.Error("expected empty after remove")
	}
}

func TestSparseSerializeRoundtrip(t *testing.T) {
	idx := NewSparseIndex()
	idx.AddDocument(1, Tokenize("machine learning"), 2)
	idx.AddDocument(2, Tokenize("deep learning"), 2)

	data, err := idx.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DeserializeSparseIndex(data)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Len() != 2 {
		t.Errorf("restored index should have 2 docs, got %d", restored.Len())
	}
	q := Tokenize("learning")
	s1 := idx.BM25Score(q, 1)
	s2 := restored.BM25Score(q, 1)
	if math.Abs(float64(s1-s2)) > 1e-6 {
		t.Errorf("BM25 scores should match after roundtrip: %f vs %f", s1, s2)
	}
}

// TestSparseIndexEntityChannelAutoPopulated verifies the third retrieval
// channel is fed automatically from indexed topic terms and stays consistent
// when documents are updated or removed.
func TestSparseIndexEntityChannelAutoPopulated(t *testing.T) {
	idx := NewSparseIndex()
	idx.AddDocument(10, []string{"memhop"}, 1)
	results := idx.EntitySearch("memhope")
	if len(results) == 0 {
		t.Fatal("entity channel should find the indexed term")
	}
	if results[0].IDHash != 10 {
		t.Fatalf("entity topic = %d, want topic 10", results[0].IDHash)
	}

	// Replacing a document updates the term association instead of keeping
	// stale terms.
	idx.AddDocument(10, []string{"agentdb"}, 1)
	if got := idx.EntitySearch("memhop"); len(got) != 0 {
		t.Fatalf("stale term memhop should be removed: %+v", got)
	}
	if got := idx.EntitySearch("agentdb"); len(got) == 0 {
		t.Fatal("replacement term agentdb should be indexed")
	}

	// Removal drops the term from the fuzzy channel.
	idx.RemoveDocument(10)
	if got := idx.EntitySearch("agentdb"); len(got) != 0 {
		t.Fatalf("removed term should disappear: %+v", got)
	}

	// Serialize/deserialize keeps the automatic entity index.
	idx.AddDocument(20, []string{"memory"}, 1)
	data, err := idx.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DeserializeSparseIndex(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.EntitySearch("memroy"); len(got) == 0 {
		t.Fatal("entity index lost after sparse index roundtrip")
	}
}
