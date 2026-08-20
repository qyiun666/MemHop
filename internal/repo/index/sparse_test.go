// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"bytes"
	"cmp"
	"reflect"
	"slices"
	"sync"
	"testing"
)

// assertEntityTerm checks the entity fuzzy-match channel of idx: term must
// be present with exactly wantIDs as its (sorted) L2 id list.
func assertEntityTerm(t *testing.T, idx *SparseIndex, term string, wantIDs []uint64) {
	t.Helper()
	_, l2IDs, ok := idx.entityIndex.ExactMatch(term)
	if !ok {
		t.Fatalf("entity term %q should exist", term)
	}
	if !slices.Equal(l2IDs, wantIDs) {
		t.Fatalf("entity term %q l2_ids = %v, want %v", term, l2IDs, wantIDs)
	}
}

// assertEntityAbsent checks the entity channel no longer contains term.
func assertEntityAbsent(t *testing.T, idx *SparseIndex, term string) {
	t.Helper()
	if _, _, ok := idx.entityIndex.ExactMatch(term); ok {
		t.Fatalf("entity term %q should not exist", term)
	}
}

// sortScoredByID normalizes EntitySearch output (score-desc, unstable for
// ties) into a deterministic id-asc order for comparison.
func sortScoredByID(docs []ScoredDoc) []ScoredDoc {
	out := slices.Clone(docs)
	slices.SortFunc(out, func(a, b ScoredDoc) int {
		return cmp.Compare(a.IDHash, b.IDHash)
	})
	return out
}

// TestSparseWriteDefersSort verifies the core lazy-sort contract: a write
// only marks the term dirty, the entity channel stays untouched, and the
// first EntitySearch resyncs it exactly once.
func TestSparseWriteDefersSort(t *testing.T) {
	idx := NewSparseIndex()
	idx.AddDocument(10, []string{"memhop"}, 1)

	if len(idx.dirtyTerms) != 1 {
		t.Fatalf("expected 1 dirty term after add, got %v", idx.dirtyTerms)
	}
	if _, ok := idx.dirtyTerms["memhop"]; !ok {
		t.Fatalf("term memhop should be dirty, got %v", idx.dirtyTerms)
	}
	// Posting is updated immediately, entity channel is not.
	if _, tfOK := idx.postings["memhop"].TermFreq[10]; !tfOK {
		t.Fatal("posting must be updated on write")
	}
	assertEntityAbsent(t, idx, "memhop")

	_ = idx.EntitySearch("memhope")
	if len(idx.dirtyTerms) != 0 {
		t.Fatalf("expected dirty cleared after read, got %v", idx.dirtyTerms)
	}
	assertEntityTerm(t, idx, "memhop", []uint64{10})

	// A second read with nothing dirty must not resync again (no-op).
	_ = idx.EntitySearch("memhope")
	if len(idx.dirtyTerms) != 0 {
		t.Fatalf("read with no dirty terms must stay clean, got %v", idx.dirtyTerms)
	}
}

// TestSparseRemoveDefersResync verifies removal marks the term dirty and the
// entity entry only disappears after the next read.
func TestSparseRemoveDefersResync(t *testing.T) {
	idx := NewSparseIndex()
	idx.AddDocument(10, []string{"memhop"}, 1)
	_ = idx.EntitySearch("sync") // resync

	idx.RemoveDocument(10)
	if len(idx.dirtyTerms) != 1 {
		t.Fatalf("expected 1 dirty term after remove, got %v", idx.dirtyTerms)
	}
	// Stale entity entry survives until the next read.
	assertEntityTerm(t, idx, "memhop", []uint64{10})

	_ = idx.EntitySearch("memhop")
	if len(idx.dirtyTerms) != 0 {
		t.Fatalf("expected dirty cleared after read, got %v", idx.dirtyTerms)
	}
	assertEntityAbsent(t, idx, "memhop")
}

// TestSparseLazySortMatchesEagerSync runs the same Add/Remove/Replace
// interleaving on two indexes: one resyncs eagerly after every write (old
// behavior), the other defers everything to the final read (new behavior).
// EntitySearch results and serialized bytes must be identical.
func TestSparseLazySortMatchesEagerSync(t *testing.T) {
	type op struct {
		kind  string // add | remove
		id    uint64
		terms []string
	}
	ops := []op{
		{"add", 10, []string{"memhop", "agent"}},
		{"add", 20, []string{"memhop"}},
		{"add", 30, []string{"memory", "agent"}},
		{"add", 10, []string{"agentdb"}}, // replace doc 10
		{"remove", 30, nil},
		{"add", 40, []string{"memhop", "memory"}},
		{"remove", 20, nil},
		{"add", 50, []string{"memhop"}},
		{"remove", 40, nil},
	}

	eager := NewSparseIndex()
	lazy := NewSparseIndex()
	for _, o := range ops {
		switch o.kind {
		case "add":
			eager.AddDocument(o.id, o.terms, uint32(len(o.terms)))
			lazy.AddDocument(o.id, o.terms, uint32(len(o.terms)))
		case "remove":
			eager.RemoveDocument(o.id)
			lazy.RemoveDocument(o.id)
		}
		_ = eager.EntitySearch("sync") // eager: resync after every write
	}

	// Lazy index is still fully dirty before its first read.
	if len(lazy.dirtyTerms) == 0 {
		t.Fatal("lazy index should still have dirty terms before first read")
	}
	_ = lazy.EntitySearch("sync") // trigger the lazy resync
	if len(lazy.dirtyTerms) != 0 {
		t.Fatalf("expected dirty cleared after resync, got %v", lazy.dirtyTerms)
	}
	// For every term that ever appeared, the final entity state must match
	// the eager one.
	for term := range eager.postings {
		_, want, wantOK := eager.entityIndex.ExactMatch(term)
		_, got, gotOK := lazy.entityIndex.ExactMatch(term)
		if wantOK != gotOK {
			t.Fatalf("entity term %q existence mismatch: eager=%v lazy=%v", term, wantOK, gotOK)
		}
		if wantOK && !slices.Equal(want, got) {
			t.Fatalf("entity term %q l2_ids mismatch: eager=%v lazy=%v", term, want, got)
		}
	}
	for term := range lazy.postings {
		if _, _, ok := eager.entityIndex.ExactMatch(term); !ok {
			t.Fatalf("entity term %q missing in eager index", term)
		}
	}
	// A removed term's entity entry must be gone after the lazy resync too.
	assertEntityAbsent(t, lazy, "memory")

	// Query results must match exactly (normalized to deterministic order).
	query := "memhop memory agent agentdb"
	gotEager := sortScoredByID(eager.EntitySearch(query))
	gotLazy := sortScoredByID(lazy.EntitySearch(query))
	if !reflect.DeepEqual(gotEager, gotLazy) {
		t.Fatalf("EntitySearch mismatch:\neager=%+v\nlazy =%+v", gotEager, gotLazy)
	}

	// Snapshot bytes must match too: both fully synced.
	bEager, err := eager.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	bLazy, err := lazy.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bEager, bLazy) {
		t.Fatalf("serialized bytes differ:\neager=%d bytes\nlazy =%d bytes", len(bEager), len(bLazy))
	}
}

// TestSparseSerializeForcesSort verifies Serialize resyncs dirty terms
// (snapshot byte compatibility), that the restored index starts clean, and
// that the lazy path keeps working after a roundtrip.
func TestSparseSerializeForcesSort(t *testing.T) {
	idx := NewSparseIndex()
	idx.AddDocument(10, []string{"memhop"}, 1)
	idx.AddDocument(20, []string{"memory"}, 1)
	if len(idx.dirtyTerms) != 2 {
		t.Fatalf("expected 2 dirty terms before serialize, got %v", idx.dirtyTerms)
	}
	assertEntityAbsent(t, idx, "memhop")

	data, err := idx.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.dirtyTerms) != 0 {
		t.Fatalf("serialize must clear dirty terms, got %v", idx.dirtyTerms)
	}
	assertEntityTerm(t, idx, "memhop", []uint64{10})
	assertEntityTerm(t, idx, "memory", []uint64{20})

	restored, err := DeserializeSparseIndex(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.dirtyTerms) != 0 {
		t.Fatalf("restored index must start clean, got %v", restored.dirtyTerms)
	}
	assertEntityTerm(t, restored, "memhop", []uint64{10})
	assertEntityTerm(t, restored, "memory", []uint64{20})

	// Writes after restore go through the lazy path again.
	restored.AddDocument(20, []string{"agentdb"}, 1)
	if len(restored.dirtyTerms) == 0 {
		t.Fatal("expected dirty after post-restore write")
	}
	if got := restored.EntitySearch("agentdb"); len(got) == 0 {
		t.Fatal("new term should be findable after lazy resync")
	}
	if got := restored.EntitySearch("memory"); len(got) != 0 {
		t.Fatalf("replaced term should be gone, got %+v", got)
	}
	assertEntityAbsent(t, restored, "memory")
}

// TestSparseSerializeDeterministicBytes verifies that serializing a dirty
// index produces the exact same bytes as serializing an already-synced one
// with the same contents.
func TestSparseSerializeDeterministicBytes(t *testing.T) {
	build := func(syncFirst bool) *SparseIndex {
		idx := NewSparseIndex()
		idx.AddDocument(10, []string{"memhop", "memory"}, 2)
		idx.AddDocument(20, []string{"memhop"}, 1)
		idx.RemoveDocument(20)
		if syncFirst {
			_ = idx.EntitySearch("sync")
		}
		return idx
	}
	dirty := build(false)
	synced := build(true)
	if len(dirty.dirtyTerms) == 0 {
		t.Fatal("expected dirty index")
	}
	bDirty, err := dirty.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	bSynced, err := synced.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bDirty, bSynced) {
		t.Fatalf("dirty serialize (%d bytes) != synced serialize (%d bytes)", len(bDirty), len(bSynced))
	}
}

// TestSparseConcurrentReadWrite is a race-detector smoke test mirroring the
// real usage in scenefind.go: EntitySearch runs concurrently with writes and
// snapshot serialization.
func TestSparseConcurrentReadWrite(t *testing.T) {
	idx := NewSparseIndex()
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func(base uint64) {
			defer wg.Done()
			for i := range 50 {
				id := base + uint64(i)
				idx.AddDocument(id, []string{"memhop", "memory", "agent"}, 3)
				_ = idx.EntitySearch("memhop")
				if i%10 == 0 {
					if _, err := idx.Serialize(); err != nil {
						t.Errorf("serialize: %v", err)
						return
					}
				}
				idx.RemoveDocument(id)
			}
			_ = idx.EntitySearch("sync") // drain remaining dirty terms
		}(uint64(w) * 1000)
	}
	wg.Wait()
	if len(idx.dirtyTerms) != 0 {
		t.Fatalf("expected no dirty terms after all writers finished, got %v", idx.dirtyTerms)
	}
}
