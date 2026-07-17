// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"path/filepath"
	"testing"

	"memhop/internal/core/model"
	"memhop/internal/core/storage"
)

// ============================================================================
// f16 tests
// ============================================================================

func TestF16Conversion(t *testing.T) {
	tests := []struct {
		name string
		f32  float32
	}{
		{"zero", 0.0},
		{"one", 1.0},
		{"negative_one", -1.0},
		{"half", 0.5},
		{"small", 0.001},
		{"large", 100.0},
		{"pi_approx", 3.14},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := F32ToF16(tt.f32)
			back := F16ToF32(h)
			if math.Abs(float64(back-tt.f32)) > 0.01*math.Abs(float64(tt.f32))+0.001 {
				t.Errorf("F16 roundtrip of %f: got %f (h=%d)", tt.f32, back, h)
			}
		})
	}
}

func TestF16SpecialValues(t *testing.T) {
	// Zero
	if F16ToF32(0x0000) != 0.0 {
		t.Error("f16 zero should be 0.0")
	}
	// Negative zero
	if F16ToF32(0x8000) != 0.0 {
		t.Error("f16 negative zero should be 0.0")
	}
	// Infinity
	inf := F16ToF32(0x7C00)
	if !math.IsInf(float64(inf), 1) {
		t.Errorf("f16 0x7C00 should be +Inf, got %f", inf)
	}
}

func TestF16SubnormalRoundtrip(t *testing.T) {
	// |v| < 2^-14 (~6.1e-5): f16 subnormal range, must not flush to zero.
	values := []float32{1e-5, 3e-5, 6e-5, -1e-5}
	for _, v := range values {
		h := F32ToF16(v)
		if h&0x7FFF == 0 {
			t.Errorf("F32ToF16(%v) flushed to zero", v)
			continue
		}
		back := F16ToF32(h)
		diff := math.Abs(float64(back - v))
		if diff > 0.01*math.Abs(float64(v))+1e-6 {
			t.Errorf("F16 subnormal roundtrip of %v: got %v (h=%d)", v, back, h)
		}
	}
	// Smallest positive subnormal: 2^-24.
	if got := F16ToF32(0x0001); got != float32(math.Pow(2, -24)) {
		t.Errorf("F16ToF32(0x0001) = %v, want 2^-24", got)
	}
	// Below 2^-24 underflows to zero.
	if h := F32ToF16(1e-9); h != 0 {
		t.Errorf("F32ToF16(1e-9) = %d, want 0 (underflow)", h)
	}
}

// ============================================================================
// cosine similarity tests
// ============================================================================

func TestCosineSimilarity(t *testing.T) {
	t.Run("identical", func(t *testing.T) {
		a := []uint16{F32ToF16(1.0), F32ToF16(0.0), F32ToF16(0.0)}
		sim := CosineSimilarity(a, a)
		if math.Abs(float64(sim-1.0)) > 1e-4 {
			t.Errorf("identical vectors should have similarity 1.0, got %f", sim)
		}
	})

	t.Run("orthogonal", func(t *testing.T) {
		a := []uint16{F32ToF16(1.0), F32ToF16(0.0), F32ToF16(0.0)}
		b := []uint16{F32ToF16(0.0), F32ToF16(1.0), F32ToF16(0.0)}
		sim := CosineSimilarity(a, b)
		if math.Abs(float64(sim)) > 1e-4 {
			t.Errorf("orthogonal vectors should have similarity ~0.0, got %f", sim)
		}
	})

	t.Run("opposite", func(t *testing.T) {
		a := []uint16{F32ToF16(1.0), F32ToF16(0.0), F32ToF16(0.0)}
		b := []uint16{F32ToF16(-1.0), F32ToF16(0.0), F32ToF16(0.0)}
		sim := CosineSimilarity(a, b)
		if math.Abs(float64(sim+1.0)) > 1e-4 {
			t.Errorf("opposite vectors should have similarity ~-1.0, got %f", sim)
		}
	})

	t.Run("zero_vector", func(t *testing.T) {
		a := []uint16{F32ToF16(0.0), F32ToF16(0.0)}
		b := []uint16{F32ToF16(1.0), F32ToF16(0.0)}
		sim := CosineSimilarity(a, b)
		if sim != 0.0 {
			t.Errorf("zero vector should give similarity 0.0, got %f", sim)
		}
	})

	t.Run("large_vector", func(t *testing.T) {
		n := 2000
		a := make([]uint16, n)
		b := make([]uint16, n)
		for i := 0; i < n; i++ {
			a[i] = F32ToF16(float32(i) * 0.001)
			b[i] = F32ToF16(float32(i) * 0.001)
		}
		sim := CosineSimilarity(a, b)
		if math.Abs(float64(sim-1.0)) > 1e-3 {
			t.Errorf("identical large vectors should have similarity ~1.0, got %f", sim)
		}
	})
}

// ============================================================================
// tokenizer tests
// ============================================================================

func TestTokenize(t *testing.T) {
	t.Run("english", func(t *testing.T) {
		tokens := Tokenize("Hello World hello")
		assertContains(t, tokens, "hello")
		assertContains(t, tokens, "world")
	})

	t.Run("chinese", func(t *testing.T) {
		tokens := Tokenize("人工智能在医疗领域的应用")
		if len(tokens) <= 1 {
			t.Errorf("Chinese text should produce multiple tokens, got %v", tokens)
		}
	})

	t.Run("camelCase", func(t *testing.T) {
		tokens := Tokenize("fetchUserData")
		assertContains(t, tokens, "fetch")
		assertContains(t, tokens, "user")
		assertContains(t, tokens, "data")
	})

	t.Run("stop_words_filtered", func(t *testing.T) {
		tokens := Tokenize("The quick brown fox")
		assertNotContains(t, tokens, "the")
		assertContains(t, tokens, "quick")
	})

	t.Run("JSONParser", func(t *testing.T) {
		tokens := Tokenize("JSONParser")
		assertContains(t, tokens, "json")
		assertContains(t, tokens, "parser")
	})

	t.Run("getUserID", func(t *testing.T) {
		tokens := Tokenize("getUserID")
		assertContains(t, tokens, "get")
		assertContains(t, tokens, "user")
		assertContains(t, tokens, "id")
	})
}

func TestTokenizeWords(t *testing.T) {
	tokens := TokenizeWords("the quick brown fox")
	assertContains(t, tokens, "the") // stop words kept

	// camelCase still split in TokenizeWords
	tokens2 := TokenizeWords("fetchUserData")
	assertContains(t, tokens2, "fetch")
}

func TestTokenizeChineseRegression(t *testing.T) {
	// CJK text must produce multiple tokens, not the whole string as one.
	tokens := Tokenize("人工智能记忆系统")
	if len(tokens) <= 1 {
		t.Errorf("CJK should split '人工智能记忆系统' into multiple tokens, got %v", tokens)
	}
}

func TestTokenizeMixedChineseEnglish(t *testing.T) {
	tokens := Tokenize("用 tokio::time::timeout 包装 async fn")
	assertContains(t, tokens, "tokio")
	assertContains(t, tokens, "timeout")
	assertContains(t, tokens, "async")
}

func TestTokenizeUnderscorePreserved(t *testing.T) {
	tokens := Tokenize("err_0x3f01")
	assertContains(t, tokens, "err")

	tokens2 := Tokenize("api_key")
	assertContains(t, tokens2, "api")
	assertContains(t, tokens2, "key")
}

func TestTokenizeAllUppercaseNotSplit(t *testing.T) {
	tokens := Tokenize("JSON")
	assertContains(t, tokens, "json")
}

func TestTokenizeChineseQuality(t *testing.T) {
	// jieba should produce "人工智能" as a single token
	tokens := Tokenize("人工智能在医疗领域的应用")
	assertContains(t, tokens, "人工智能")
	assertContains(t, tokens, "医疗")
}

func TestTokenizerEngineSelection(t *testing.T) {
	// Test that we can explicitly select gse engine
	ResetTokenizer()
	if err := InitTokenizer(EngineGse); err != nil {
		t.Fatalf("init tokenizer gse: %v", err)
	}
	tokens := Tokenize("Hello World")
	assertContains(t, tokens, "hello")
	assertContains(t, tokens, "world")

	// Reset back to default for other tests
	ResetTokenizer()
}

func TestSplitCamelCase(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"fetchUserData", []string{"fetch", "user", "data"}},
		{"JSONParser", []string{"json", "parser"}},
		{"getUserID", []string{"get", "user", "id"}},
		{"simple", []string{"simple"}},
		{"ALLCAPS", []string{"ALLCAPS"}},
		{"with_underscore", []string{"with_underscore"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitCamelCase(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("splitCamelCase(%q) = %v, want %v", tt.input, got, tt.expected)
				return
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("splitCamelCase(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.expected[i])
				}
			}
		})
	}
}

// ============================================================================
// BM25 / SparseIndex tests
// ============================================================================

func TestBM25Score(t *testing.T) {
	idx := NewSparseIndexWithParams(1.2, 0.75)
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
	for i := uint64(0); i < 10; i++ {
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
	for i := uint64(0); i < 5; i++ {
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

func TestMergeSparseIndex(t *testing.T) {
	idx1 := NewSparseIndex()
	idx1.AddDocument(1, Tokenize("rust memory safety"), 3)
	idx2 := NewSparseIndex()
	idx2.AddDocument(2, Tokenize("rust concurrency"), 2)
	idx1.Merge(idx2)
	if idx1.Len() != 2 {
		t.Errorf("merged index should have 2 docs, got %d", idx1.Len())
	}
}

// ============================================================================
// EntityIndex tests
// ============================================================================

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

func TestBKTree(t *testing.T) {
	tree := newBkTree()
	tree.insert("apple")
	tree.insert("apply")

	// Exact match
	matches := tree.search("apple", 0)
	if len(matches) != 1 {
		t.Errorf("exact search should find 1, got %d", len(matches))
	}

	// Fuzzy
	matches = tree.search("aple", 1)
	if len(matches) == 0 {
		t.Error("fuzzy search should find at least 1 match")
	}

	// Duplicate insertion
	for i := 0; i < 100; i++ {
		tree.insert("apple")
	}
	if len(tree.nodes) != 2 { // apple + apply
		t.Errorf("duplicate insertions should not create new nodes, got %d", len(tree.nodes))
	}
}

func TestLevenshteinDistance(t *testing.T) {
	if d := levenshteinDistance("kitten", "sitting"); d != 3 {
		t.Errorf("kitten→sitting should be 3, got %d", d)
	}
	if d := levenshteinDistance("abc", "abc"); d != 0 {
		t.Errorf("same string should be 0, got %d", d)
	}
	if d := levenshteinDistance("", "abc"); d != 3 {
		t.Errorf("empty→abc should be 3, got %d", d)
	}
}

// ============================================================================
// L2MetaIndex tests
// ============================================================================

func TestL2MetaIndex(t *testing.T) {
	t.Run("basic_crud", func(t *testing.T) {
		idx := NewL2MetaIndex()
		meta := &L2Meta{
			IDHash:      42,
			Title:       "test topic",
			Depth:       1,
			SceneID:     100,
			ChildrenIDs: []uint64{1, 2, 3},
			Timestamp:   2000,
		}
		idx.Update(meta)
		if idx.Len() != 1 {
			t.Errorf("expected len 1, got %d", idx.Len())
		}

		got := idx.Get(42)
		if got == nil || got.Title != "test topic" {
			t.Errorf("Get(42) should return 'test topic'")
		}

		sceneIDs := idx.GetByScene(100)
		if len(sceneIDs) != 1 || sceneIDs[0] != 42 {
			t.Errorf("GetByScene(100) should return [42], got %v", sceneIDs)
		}

		depthIDs := idx.GetBySceneDepth(100, 1)
		if len(depthIDs) != 1 {
			t.Errorf("GetBySceneDepth(100,1) should return 1 id, got %d", len(depthIDs))
		}

		removed := idx.Remove(42)
		if removed == nil || removed.Title != "test topic" {
			t.Error("Remove should return removed meta")
		}
		if idx.Len() != 0 {
			t.Error("should be empty after remove")
		}
	})

	t.Run("bm25_prescreen", func(t *testing.T) {
		idx := NewL2MetaIndex()
		sparse := NewSparseIndex()

		// Add L2 entry.
		meta := &L2Meta{IDHash: 101, Title: "rust memory search", SceneID: 0, Depth: 1}
		idx.Update(meta)
		sparse.AddDocument(101, []string{"rust", "memory", "search"}, 3)

		// Add non-L2 entry in sparse (no corresponding L2Meta).
		sparse.AddDocument(201, []string{"rust", "memory"}, 2)

		ids := idx.BM25Prescreen("rust memory", sparse, 10)
		if len(ids) != 1 || ids[0] != 101 {
			t.Errorf("prescreen should return only L2 id 101, got %v", ids)
		}
	})
}

func TestBuildL2MetaFromEngine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "l2meta.meh")
	engine, err := storage.Create(path, 768)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close(&storage.IndexSnapshotData{})

	topic := model.TopicSlot{
		ID:           101,
		SceneID:      1,
		Depth:        1,
		UserKeywords: []string{"rust", "memory", "search"},
		UserL3Refs:   []uint64{501},
		CreatedAt:    1000,
		UpdatedAt:    2000,
	}
	data, _ := json.Marshal(topic)
	engine.WriteRecord(storage.RecL2Topic, 101, data)

	l2idx := BuildL2MetaFromEngine(engine)
	if l2idx.Len() != 1 {
		t.Errorf("expected 1 L2 entry, got %d", l2idx.Len())
	}
	meta := l2idx.Get(101)
	if meta == nil {
		t.Fatal("should find meta for id 101")
	}
	if meta.Depth != 1 {
		t.Errorf("expected depth 1, got %d", meta.Depth)
	}
	if len(meta.L3Refs) != 1 || meta.L3Refs[0] != 501 {
		t.Errorf("expected L3Refs [501], got %v", meta.L3Refs)
	}
}

// ============================================================================
// helpers
// ============================================================================

func makeF16Vec(dim int, val float32) []uint16 {
	v := make([]uint16, dim)
	for i := range v {
		v[i] = F32ToF16(val)
	}
	return v
}

func encodeF16Vec(vec []uint16) []byte {
	data := make([]byte, len(vec)*2)
	for i, v := range vec {
		binary.LittleEndian.PutUint16(data[i*2:], v)
	}
	return data
}

func assertContains(t *testing.T, tokens []string, expected string) {
	t.Helper()
	for _, tok := range tokens {
		if tok == expected {
			return
		}
	}
	t.Errorf("tokens %v should contain %q", tokens, expected)
}

func assertNotContains(t *testing.T, tokens []string, unexpected string) {
	t.Helper()
	for _, tok := range tokens {
		if tok == unexpected {
			t.Errorf("tokens %v should not contain %q", tokens, unexpected)
			return
		}
	}
}
