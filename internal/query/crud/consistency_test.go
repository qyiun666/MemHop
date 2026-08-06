// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Regression tests for write→delete→search consistency across the
// in-memory indexes (L2Meta, L3Index, BM25 sparse) and the storage engine.

package crud

import (
	"encoding/json"
	"testing"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/repo/core/index"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

func sparseContainsID(sparse *index.SparseIndex, queryText string, id uint64) bool {
	for _, d := range sparse.Search(index.Tokenize(queryText), 10) {
		if d.IDHash == id {
			return true
		}
	}
	return false
}

// DeleteL3 must drop the graph's nodes from L3Index so SearchL3Nodes never
// returns dangling IDs.
func TestDeleteL3CleansL3Index(t *testing.T) {
	engine := createTestEngine(t)
	l3Idx := index.NewL3Index()

	graphID := uint64(9001)
	slot := model.HypergraphSlot{
		IDHash:    graphID,
		Name:      "ghost graph",
		Source:    model.HypergraphSource{Kind: model.SourceManual},
		CreatedAt: 1000,
		UpdatedAt: 1000,
	}
	writeGraphSlot(engine, graphID, &slot)

	for i, kw := range []string{"ghostkey1", "ghostkey2"} {
		node := model.HypergraphNode{
			IDHash:     uint64(9101 + i),
			GraphID:    graphID,
			Title:      "node " + kw,
			NodeType:   "ghosttype",
			Content:    "content",
			Keywords:   []string{kw},
			Importance: 0.5,
			CreatedAt:  1000,
			UpdatedAt:  1000,
		}
		data, err := node.MarshalJSON()
		if err != nil {
			t.Fatalf("marshal node: %v", err)
		}
		if _, err := engine.WriteRecord(storage.RecL3GraphNode, node.IDHash, data); err != nil {
			t.Fatalf("write node: %v", err)
		}
		l3Idx.AddNode(&node)
	}

	// Sanity: indexed before delete.
	if got := l3Idx.SearchByKeyword("ghostkey1", 10); len(got) != 1 {
		t.Fatalf("expected 1 node in L3Index before delete, got %d", len(got))
	}

	if err := DeleteL3(engine, l3Idx, hash.FormatHash(graphID)); err != nil {
		t.Fatalf("delete l3: %v", err)
	}

	for _, kw := range []string{"ghostkey1", "ghostkey2"} {
		if got := l3Idx.SearchByKeyword(kw, 10); len(got) != 0 {
			t.Errorf("ghost node in L3Index after DeleteL3 (keyword %q): %v", kw, got)
		}
	}
	if got := l3Idx.SearchByType("ghosttype", graphID, 10); len(got) != 0 {
		t.Errorf("ghost nodes in L3Index type search after DeleteL3: %v", got)
	}
	res, err := SearchL3Nodes(l3Idx, engine, L3SearchQuery{Keyword: "ghostkey1"})
	if err != nil {
		t.Fatalf("search l3 nodes: %v", err)
	}
	if len(res.Nodes) != 0 {
		t.Errorf("SearchL3Nodes returned dangling IDs after DeleteL3: %v", res.Nodes)
	}
}

// AppendDialogueL4 must rebuild the topic's BM25 document so newly merged
// keywords become searchable.
func TestAppendDialogueL4IndexesKeywords(t *testing.T) {
	engine := createTestEngine(t)
	sparse := index.NewSparseIndex()
	topicID := uint64(2301)
	writeTestTopic(t, engine, topicID, "original title")

	if _, err := AppendDialogueL4(engine, sparse, topicID, "hello from user", 0, []string{"brandnewkeyword"}, 1000000); err != nil {
		t.Fatalf("append dialogue l4: %v", err)
	}

	if !sparseContainsID(sparse, "brandnewkeyword", topicID) {
		t.Error("new keyword not searchable via BM25 after AppendDialogueL4")
	}
	if !sparseContainsID(sparse, "original", topicID) {
		t.Error("existing keyword lost from BM25 after AppendDialogueL4")
	}
	topic, err := record.ReadTopicSlot(engine, topicID)
	if err != nil {
		t.Fatalf("load topic: %v", err)
	}
	found := false
	for _, kw := range topic.UserKeywords {
		if kw == "brandnewkeyword" {
			found = true
		}
	}
	if !found {
		t.Error("keyword not merged into topic UserKeywords")
	}
	if len(topic.UserL4Refs) != 1 {
		t.Errorf("expected 1 user L4 ref, got %d", len(topic.UserL4Refs))
	}
}

// MergeL2 must remove the secondary from all indexes and cascade-delete its
// associated L1 nodes, while refreshing the primary's L2Meta entry.
func TestMergeL2CleansIndexes(t *testing.T) {
	engine := createTestEngine(t)
	sparse := index.NewSparseIndex()
	l1Rev := index.NewL1ReverseIndex()

	idA := uint64(2001)
	idB := uint64(2002)
	writeTestTopic(t, engine, idA, "merge primary")
	writeTestTopic(t, engine, idB, "merge secondary")
	termsA := index.Tokenize("merge primary")
	sparse.AddDocument(idA, termsA, uint32(len(termsA)))
	termsB := index.Tokenize("merge secondary")
	sparse.AddDocument(idB, termsB, uint32(len(termsB)))
	l2Meta := index.BuildL2MetaFromEngine(engine)

	// L1 node associated with the secondary topic.
	l1NodeID := uint64(2101)
	node := model.SceneNode{IDHash: l1NodeID, SceneID: idB, CreatedAt: 1, UpdatedAt: 1, EdgeIDs: []uint64{}}
	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal node: %v", err)
	}
	if _, err := engine.WriteRecord(storage.RecL1SceneNode, l1NodeID, data); err != nil {
		t.Fatalf("write l1 node: %v", err)
	}
	l1Rev.Add(idB, l1NodeID)

	res, err := MergeL2(engine, l1Rev, sparse, l2Meta, hash.FormatHash(idA), []string{hash.FormatHash(idB)})
	if err != nil {
		t.Fatalf("merge l2: %v", err)
	}
	if res.MergedCount != 1 {
		t.Errorf("expected 1 merged, got %d", res.MergedCount)
	}
	if engine.Contains(idB) {
		t.Error("secondary record still in engine after merge")
	}
	if l2Meta.Get(idB) != nil {
		t.Error("ghost secondary entry in L2Meta after merge")
	}
	if l2Meta.Get(idA) == nil {
		t.Error("primary entry missing from L2Meta after merge")
	}
	if sparseContainsID(sparse, "secondary", idB) {
		t.Error("ghost secondary document in BM25 after merge")
	}
	if engine.Contains(l1NodeID) {
		t.Error("secondary's L1 node still in engine after merge")
	}
	if got := l1Rev.FindAssociated(map[uint64]struct{}{idB: {}}); len(got) != 0 {
		t.Errorf("ghost l1Reverse entries for secondary after merge: %v", got)
	}
}

// Merging a topic into itself must be a no-op (no "x | x" summary, record
// kept), and repeating it stays idempotent.
func TestMergeL2SelfMergeIdempotent(t *testing.T) {
	engine := createTestEngine(t)
	sparse := index.NewSparseIndex()
	l1Rev := index.NewL1ReverseIndex()
	l2Meta := index.NewL2MetaIndex()

	idA := uint64(2201)
	topic := model.TopicSlot{
		ID:            idA,
		Depth:         1,
		UserKeywords:  []string{"self"},
		UserTimestamp: 1000,
		UserL4Refs:    []uint64{},
		UserL3Refs:    []uint64{},
		AgentKeywords: []string{},
		AgentL4Refs:   []uint64{},
		AgentL3Refs:   []uint64{},
		FusedKeywords: []string{},
		ChildrenIDs:   []uint64{},
	}
	if err := record.WriteTopicSlot(engine, idA, &topic); err != nil {
		t.Fatalf("write topic: %v", err)
	}

	hexID := hash.FormatHash(idA)
	for round := 1; round <= 2; round++ {
		res, err := MergeL2(engine, l1Rev, sparse, l2Meta, hexID, []string{hexID})
		if err != nil {
			t.Fatalf("self merge round %d: %v", round, err)
		}
		if res.MergedCount != 0 {
			t.Errorf("round %d: expected 0 merged for self merge, got %d", round, res.MergedCount)
		}
		_, err = record.ReadTopicSlot(engine, idA)
		if err != nil {
			t.Fatalf("round %d: primary record lost after self merge: %v", round, err)
		}
	}
}
