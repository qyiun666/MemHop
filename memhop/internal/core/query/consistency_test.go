// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Regression tests for write→delete→search consistency across the
// in-memory indexes (L2Meta, L3Index, BM25 sparse) and the storage engine.

package query

import (
	"encoding/json"
	"testing"

	"github.com/qyiun666/memhop/memhop/internal/core/index"
	"github.com/qyiun666/memhop/memhop/internal/core/l3"
	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
	"github.com/qyiun666/memhop/memhop/internal/hash"
)

func batchStoreOne(t *testing.T, deps *BatchDeps, content, kw, label string) {
	t.Helper()
	batch := StoreBatch{
		Items: []StoreItem{
			{Content: content, Keywords: []string{kw}, TopicLabel: &label, Score: 0.8},
		},
	}
	if _, err := BatchStore(batch, deps); err != nil {
		t.Fatalf("batch store: %v", err)
	}
}

func sparseContainsID(sparse *index.SparseIndex, queryText string, id uint64) bool {
	for _, d := range sparse.Search(index.Tokenize(queryText), 10) {
		if d.IDHash == id {
			return true
		}
	}
	return false
}

// Batch-written topics must be visible to normal search (candidate set is
// built from L2Meta) without waiting for an index rebuild.
func TestBatchStoreUpdatesL2Meta(t *testing.T) {
	engine := createTestEngine(t)
	sparse := index.NewSparseIndex()
	l2Meta := index.NewL2MetaIndex()
	deps := &BatchDeps{Engine: engine, SparseIndex: sparse, L2Meta: l2Meta, VectorDim: 768}

	label := "l2meta-topic"
	batchStoreOne(t, deps, "alpha content", "alpha", label)

	topicID := hash.HashID(label)
	meta := l2Meta.Get(topicID)
	if meta == nil {
		t.Fatal("batch-written topic missing from L2MetaIndex")
	}
	if meta.Title == "" {
		t.Error("expected non-empty L2Meta title")
	}
	cands := BuildCandidateSet(l2Meta, sparse, nil)
	if _, ok := cands[topicID]; !ok {
		t.Error("batch-written topic not in search candidate set before rebuild")
	}
	if !sparseContainsID(sparse, "alpha", topicID) {
		t.Error("batch-written topic not searchable via BM25")
	}
}

// DeleteL2 must remove the topic from L2Meta, BM25, l1Reverse and the
// engine, leaving no ghost IDs behind.
func TestDeleteL2CleansIndexes(t *testing.T) {
	engine := createTestEngine(t)
	sparse := index.NewSparseIndex()
	l2Meta := index.NewL2MetaIndex()
	deps := &BatchDeps{Engine: engine, SparseIndex: sparse, L2Meta: l2Meta, VectorDim: 768}

	label := "ghost-topic"
	batchStoreOne(t, deps, "ghost content", "ghostkw", label)

	topicID := hash.HashID(label)
	l1NodeID := L1NodeIDHash("ghost content")
	l1Rev := BuildL1ReverseIndex(engine)

	// Sanity: fully indexed before delete.
	if l2Meta.Get(topicID) == nil {
		t.Fatal("expected topic in L2Meta before delete")
	}
	if !sparseContainsID(sparse, "ghostkw", topicID) {
		t.Fatal("expected topic in BM25 before delete")
	}
	if got := l1Rev.FindAssociated(map[uint64]struct{}{topicID: {}}); len(got) == 0 {
		t.Fatal("expected associated L1 node before delete")
	}

	if err := DeleteL2(engine, l1Rev, sparse, l2Meta, hash.FormatHash(topicID)); err != nil {
		t.Fatalf("delete l2: %v", err)
	}

	if l2Meta.Get(topicID) != nil {
		t.Error("ghost entry in L2Meta after DeleteL2")
	}
	if got := l2Meta.GetByScene(0); len(got) != 0 {
		t.Errorf("ghost scene entries in L2Meta after DeleteL2: %v", got)
	}
	if sparseContainsID(sparse, "ghostkw", topicID) {
		t.Error("ghost topic document in BM25 after DeleteL2")
	}
	if engine.Contains(topicID) {
		t.Error("topic record still in engine after DeleteL2")
	}
	if engine.Contains(l1NodeID) {
		t.Error("associated L1 node still in engine after DeleteL2")
	}
	if got := l1Rev.FindAssociated(map[uint64]struct{}{topicID: {}}); len(got) != 0 {
		t.Errorf("ghost l1Reverse entries after DeleteL2: %v", got)
	}
}

// DeleteL3 must drop the graph's nodes from L3Index so SearchL3Nodes never
// returns dangling IDs.
func TestDeleteL3CleansL3Index(t *testing.T) {
	engine := createTestEngine(t)
	l3Idx := l3.NewL3Index()

	graphID := uint64(9001)
	slot := model.HypergraphSlot{
		IDHash:    graphID,
		Name:      "ghost graph",
		Source:    model.HypergraphSource{Kind: model.SourceManual},
		CreatedAt: 1000,
		UpdatedAt: 1000,
		Version:   1,
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
			Version:    1,
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

	if _, err := AppendDialogueL4(engine, sparse, topicID, "hello from user", 0, []string{"brandnewkeyword"}); err != nil {
		t.Fatalf("append dialogue l4: %v", err)
	}

	if !sparseContainsID(sparse, "brandnewkeyword", topicID) {
		t.Error("new keyword not searchable via BM25 after AppendDialogueL4")
	}
	if !sparseContainsID(sparse, "original", topicID) {
		t.Error("existing keyword lost from BM25 after AppendDialogueL4")
	}
	topic, err := loadTopic(engine, topicID)
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
	l1Rev := NewL1ReverseIndex()

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
	node := model.ContextNode{IDHash: l1NodeID, ContextID: idB, CreatedAt: 1, UpdatedAt: 1, Version: 1, EdgePtrs: []uint64{}}
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
	l1Rev := NewL1ReverseIndex()
	l2Meta := index.NewL2MetaIndex()

	idA := uint64(2201)
	summary := "x"
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
		FusedSummary:  &summary,
		ChildrenIDs:   []uint64{},
		CreatedAt:     1000,
		UpdatedAt:     1000,
		Version:       1,
	}
	if err := writeTopic(engine, idA, &topic); err != nil {
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
		got, err := loadTopic(engine, idA)
		if err != nil {
			t.Fatalf("round %d: primary record lost after self merge: %v", round, err)
		}
		if got.FusedSummary == nil || *got.FusedSummary != "x" {
			t.Errorf("round %d: self merge corrupted summary: %v", round, got.FusedSummary)
		}
	}
}

// Batch hyperedge IDs must be content-derived: distinct batches never
// overwrite each other's edges, re-import of the same batch is idempotent.
func TestBatchHyperedgeIDsUniqueAcrossBatches(t *testing.T) {
	engine := createTestEngine(t)
	sparse := index.NewSparseIndex()
	deps := &BatchDeps{Engine: engine, SparseIndex: sparse, VectorDim: 768}

	label1, label2 := "edge-topic-1", "edge-topic-2"
	batch1 := StoreBatch{Items: []StoreItem{
		{Content: "edge alpha", Keywords: []string{"a"}, TopicLabel: &label1, Score: 0.5},
		{Content: "edge beta", Keywords: []string{"b"}, TopicLabel: &label1, Score: 0.5},
	}}
	batch2 := StoreBatch{Items: []StoreItem{
		{Content: "edge gamma", Keywords: []string{"c"}, TopicLabel: &label2, Score: 0.5},
		{Content: "edge delta", Keywords: []string{"d"}, TopicLabel: &label2, Score: 0.5},
	}}
	if _, err := BatchStore(batch1, deps); err != nil {
		t.Fatalf("batch1: %v", err)
	}
	if _, err := BatchStore(batch2, deps); err != nil {
		t.Fatalf("batch2: %v", err)
	}

	countEdges := func() (total int, assocIDs map[uint64]bool) {
		assocIDs = make(map[uint64]bool)
		engine.IterIndex(func(idHash, _ uint64) bool {
			rt, data, err := engine.ReadRecord(idHash)
			if err != nil || rt != storage.RecL1Hyperedge {
				return true
			}
			total++
			var edge model.HyperedgeSlot
			if json.Unmarshal(data, &edge) == nil && edge.Kind == model.HyperCoOccurrence {
				assocIDs[idHash] = true
			}
			return true
		})
		return total, assocIDs
	}

	total, assocIDs := countEdges()
	if total != 4 {
		t.Errorf("expected 4 hyperedges after 2 batches (overwrite corruption), got %d", total)
	}
	if len(assocIDs) != 2 {
		t.Errorf("expected 2 distinct co-occurrence edges, got %d", len(assocIDs))
	}

	// Re-importing the same batch must not create new edges.
	if _, err := BatchStore(batch1, deps); err != nil {
		t.Fatalf("batch1 re-import: %v", err)
	}
	totalAfter, _ := countEdges()
	if totalAfter != 4 {
		t.Errorf("expected idempotent re-import (4 edges), got %d", totalAfter)
	}
}

// Fallback centroids must be content-addressed: distinct node vectors yield
// distinct vector records, identical input yields the same record.
func TestAverageNodeCentroidContentAddressed(t *testing.T) {
	engine := createTestEngine(t)
	deps := &BatchDeps{Engine: engine, VectorDim: 2}

	writeVecNode := func(nodeID, vecID uint64, vec []uint16) {
		t.Helper()
		if _, err := engine.WriteRecord(storage.RecVecCentroid, vecID, f16SliceToBytes(vec)); err != nil {
			t.Fatalf("write vector: %v", err)
		}
		node := model.ContextNode{IDHash: nodeID, VectorPageRef: vecID, CreatedAt: 1, UpdatedAt: 1, Version: 1, EdgePtrs: []uint64{}}
		data, err := json.Marshal(node)
		if err != nil {
			t.Fatalf("marshal node: %v", err)
		}
		if _, err := engine.WriteRecord(storage.RecL1SceneNode, nodeID, data); err != nil {
			t.Fatalf("write node: %v", err)
		}
	}
	writeVecNode(6001, 7001, []uint16{index.F32ToF16(1), index.F32ToF16(0)})
	writeVecNode(6002, 7002, []uint16{index.F32ToF16(0), index.F32ToF16(1)})

	ref1, err := averageNodeCentroid(deps, []uint64{6001})
	if err != nil {
		t.Fatal(err)
	}
	ref2, err := averageNodeCentroid(deps, []uint64{6002})
	if err != nil {
		t.Fatal(err)
	}
	if ref1 == 0 || ref2 == 0 {
		t.Fatalf("expected non-zero centroid refs, got %d and %d", ref1, ref2)
	}
	if ref1 == ref2 {
		t.Error("distinct centroids share one vector record (mutual overwrite)")
	}
	again, err := averageNodeCentroid(deps, []uint64{6001})
	if err != nil {
		t.Fatal(err)
	}
	if again != ref1 {
		t.Errorf("same input not idempotent: %d != %d", again, ref1)
	}
}

// backfillContextID must never overwrite records that are not L1 nodes
// (legacy ID collision guard), while still updating real L1 nodes.
func TestBackfillContextIDTypeGuard(t *testing.T) {
	engine := createTestEngine(t)

	// A topic record sits at the ID handed to backfill (collision scenario).
	topicID := uint64(2401)
	writeTestTopic(t, engine, topicID, "colliding topic")
	backfillContextID(engine, []uint64{topicID}, 9999, 12345)

	rt, _, err := engine.ReadRecord(topicID)
	if err != nil || rt != storage.RecL2Topic {
		t.Fatalf("topic record overwritten by backfill (rt=%d, err=%v)", rt, err)
	}
	topic, err := loadTopic(engine, topicID)
	if err != nil {
		t.Fatalf("topic record corrupted by backfill: %v", err)
	}
	if topic.UserKeywords[0] != "colliding topic" {
		t.Errorf("topic content altered by backfill: %v", topic.UserKeywords)
	}

	// Positive control: a real L1 node still gets its ContextID backfilled.
	l1NodeID := uint64(2501)
	node := model.ContextNode{IDHash: l1NodeID, CreatedAt: 1, UpdatedAt: 1, Version: 1, EdgePtrs: []uint64{}}
	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal node: %v", err)
	}
	if _, err := engine.WriteRecord(storage.RecL1SceneNode, l1NodeID, data); err != nil {
		t.Fatalf("write l1 node: %v", err)
	}
	backfillContextID(engine, []uint64{l1NodeID}, 8888, 12345)

	_, nodeData, err := engine.ReadRecord(l1NodeID)
	if err != nil {
		t.Fatalf("read l1 node: %v", err)
	}
	var got model.ContextNode
	if err := json.Unmarshal(nodeData, &got); err != nil {
		t.Fatalf("unmarshal l1 node: %v", err)
	}
	if got.ContextID != 8888 {
		t.Errorf("expected backfilled ContextID 8888, got %d", got.ContextID)
	}
}

// L1 node IDs must live in their own namespace, disjoint from L2 topic IDs.
func TestL1NodeIDPrefix(t *testing.T) {
	if L1NodeIDHash("x") == hash.HashID("x") {
		t.Error("L1 node ID must be namespaced, not raw HashID(text)")
	}
	if L1NodeIDHash("x") != hash.HashID("l1:x") {
		t.Error("L1NodeIDHash must be HashID(\"l1:\"+text)")
	}

	engine := createTestEngine(t)
	sparse := index.NewSparseIndex()
	deps := &BatchDeps{Engine: engine, SparseIndex: sparse, VectorDim: 768}
	batchStoreOne(t, deps, "namespaced content", "ns", "ns-topic")

	if !engine.Contains(L1NodeIDHash("namespaced content")) {
		t.Error("L1 node not stored under prefixed ID")
	}
	if engine.Contains(hash.HashID("namespaced content")) {
		t.Error("L1 node unexpectedly stored under raw (unprefixed) ID")
	}
}
