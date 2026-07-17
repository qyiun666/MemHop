// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package query

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"memhop/internal/core/index"
	"memhop/internal/core/model"
	"memhop/internal/core/storage"
	"memhop/internal/hash"
)

func createTestEngine(t *testing.T) *storage.StorageEngine {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.meh")
	engine, err := storage.Create(path, 768)
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() {
		snap := &storage.IndexSnapshotData{}
		engine.Close(snap)
	})
	return engine
}

func writeTestTopic(t *testing.T, engine *storage.StorageEngine, id uint64, title string) {
	t.Helper()
	topic := model.TopicSlot{
		ID:            id,
		Depth:         1,
		UserKeywords:  []string{title},
		UserTimestamp: 1000,
		UserL4Refs:    []uint64{},
		UserL3Refs:    []uint64{},
		AgentKeywords: []string{},
		AgentL4Refs:   []uint64{},
		AgentL3Refs:   []uint64{},
		FusedKeywords: []string{},
		ChildrenIDs:   []uint64{},
		CreatedAt:     1000,
		UpdatedAt:     1000,
		Version:       1,
	}
	if err := writeTopic(engine, id, &topic); err != nil {
		t.Fatalf("write topic: %v", err)
	}
}

func TestSearchEmptyDB(t *testing.T) {
	engine := createTestEngine(t)
	sparse := index.NewSparseIndex()
	l2Meta := index.NewL2MetaIndex()
	l1Rev := NewL1ReverseIndex()

	deps := &SearchDeps{
		SparseIndex: sparse,
		L2Meta:      l2Meta,
		VectorDim:   768,
		Engine:      engine,
		Encoder:     nil,
		L1Reverse:   l1Rev,
	}
	q := SearchQuery{Text: "test query"}
	result, err := SearchContext(q, deps)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(result.Contexts) != 0 {
		t.Errorf("expected 0 contexts, got %d", len(result.Contexts))
	}
	// Verify new result structure: has Crystals
	if result.Crystals == nil {
		t.Error("expected Crystals to be initialized")
	}
}

func TestSearchWriteAndRecall(t *testing.T) {
	engine := createTestEngine(t)
	sparse := index.NewSparseIndex()

	// Write a topic and index it
	writeTestTopic(t, engine, 1001, "Go programming language")
	terms := index.Tokenize("go programming language")
	sparse.AddDocument(1001, terms, uint32(len(terms)))

	l2Meta := index.BuildL2MetaFromEngine(engine)
	l1Rev := BuildL1ReverseIndex(engine)

	deps := &SearchDeps{
		SparseIndex: sparse,
		L2Meta:      l2Meta,
		VectorDim:   768,
		Engine:      engine,
		Encoder:     nil,
		L1Reverse:   l1Rev,
	}
	q := SearchQuery{Text: "Go programming", AutoCreate: true}
	result, err := SearchContext(q, deps)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(result.Contexts) == 0 {
		t.Error("expected at least 1 context, got 0")
	}
	// Verify depth=1 filtering and new result structure
	for _, ctx := range result.Contexts {
		if ctx.Depth != 1 {
			t.Errorf("expected depth=1, got %d", ctx.Depth)
		}
	}
}

func TestRRFMerge(t *testing.T) {
	bm25 := []index.ScoredDoc{
		{IDHash: 1, Score: 5.0},
		{IDHash: 2, Score: 3.0},
		{IDHash: 3, Score: 1.0},
	}
	vector := []index.ScoredDoc{
		{IDHash: 2, Score: 0.9},
		{IDHash: 3, Score: 0.8},
		{IDHash: 1, Score: 0.5},
	}
	merged := RRFMerge(bm25, vector, 60.0)
	if len(merged) != 3 {
		t.Fatalf("expected 3 merged docs, got %d", len(merged))
	}
	// ID 1: rank 1 in bm25 → 1/61, rank 3 in vector → 1/63 → total ≈ 0.0323
	// ID 2: rank 2 in bm25 → 1/62, rank 1 in vector → 1/61 → total ≈ 0.0325
	// ID 2 should be top since 1/62+1/61 > 1/61+1/63
	if merged[0].IDHash != 2 {
		t.Errorf("expected top doc ID 2, got %d", merged[0].IDHash)
	}
}

func TestL2CRUD(t *testing.T) {
	engine := createTestEngine(t)
	sparse := index.NewSparseIndex()
	l1Rev := NewL1ReverseIndex()
	l2Meta := index.NewL2MetaIndex()

	// Create
	writeTestTopic(t, engine, 2001, "Rust refactoring")
	terms := index.Tokenize("rust refactoring")
	sparse.AddDocument(2001, terms, uint32(len(terms)))

	// Read
	hexID := hash.FormatHash(2001)
	got, err := GetL2(engine, hexID)
	if err != nil {
		t.Fatalf("get l2: %v", err)
	}
	if got.UserKeywords[0] != "Rust refactoring" {
		t.Errorf("unexpected keyword: %s", got.UserKeywords[0])
	}

	// Update
	newKws := []string{"Updated title"}
	detail, err := UpdateL2(engine, sparse, hexID, UpdateL2Fields{UserKeywords: newKws})
	if err != nil {
		t.Fatalf("update l2: %v", err)
	}
	if detail.UserKeywords[0] != "Updated title" {
		t.Errorf("update failed: %s", detail.UserKeywords[0])
	}

	// List
	list, err := ListL2(engine, TopicListQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list l2: %v", err)
	}
	if list.Total != 1 {
		t.Errorf("expected 1 topic, got %d", list.Total)
	}

	// Delete
	if err := DeleteL2(engine, l1Rev, sparse, l2Meta, hexID); err != nil {
		t.Fatalf("delete l2: %v", err)
	}
	_, err = GetL2(engine, hexID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestBatchStore(t *testing.T) {
	engine := createTestEngine(t)
	sparse := index.NewSparseIndex()

	deps := &BatchDeps{
		Engine:      engine,
		SparseIndex: sparse,
		VectorDim:   768,
		Encoder:     nil,
	}

	label := "test-topic"
	batch := StoreBatch{
		Items: []StoreItem{
			{Content: "hello world", Keywords: []string{"hello"}, TopicLabel: &label, Score: 0.8},
			{Content: "foo bar", Keywords: []string{"foo"}, TopicLabel: &label, Score: 0.5},
		},
	}
	report, err := BatchStore(batch, deps)
	if err != nil {
		t.Fatalf("batch store: %v", err)
	}
	if report.L4Docs != 2 {
		t.Errorf("expected 2 L4 docs, got %d", report.L4Docs)
	}
	if report.L1NodesCreated != 2 {
		t.Errorf("expected 2 L1 nodes created, got %d", report.L1NodesCreated)
	}
	// No encoder → no vector dedup, both items stored
	if report.DedupSkipped != 0 {
		t.Errorf("expected 0 dedup skipped (no encoder), got %d", report.DedupSkipped)
	}
	if report.L2TopicsUpdated != 1 {
		t.Errorf("expected 1 L2 topic, got %d", report.L2TopicsUpdated)
	}
}

func TestImportMemory(t *testing.T) {
	engine := createTestEngine(t)
	sparse := index.NewSparseIndex()

	// Import profile
	name := "TestBot"
	role := "Assistant"
	profileReq := ImportRequest{
		TargetLayer: TargetProfile,
		Mode:        ImportMerge,
		Data: ImportData{
			Profile: &ProfileImportData{Name: &name, Role: &role},
		},
	}
	result, err := ImportMemory(engine, sparse, nil, nil, nil, profileReq)
	if err != nil {
		t.Fatalf("import profile: %v", err)
	}
	if result.Status != ImportSuccess {
		t.Errorf("expected success, got %s", result.Status)
	}

	// Import topics
	summary := "test summary"
	topicReq := ImportRequest{
		TargetLayer: TargetTopic,
		Mode:        ImportMerge,
		Data: ImportData{
			Topics: []TopicImportItem{
				{Title: "Test Topic", Summary: &summary, Keywords: []string{"test"}},
			},
		},
	}
	result, err = ImportMemory(engine, sparse, nil, nil, nil, topicReq)
	if err != nil {
		t.Fatalf("import topics: %v", err)
	}
	if result.NodeCount != 1 {
		t.Errorf("expected 1 created, got %d", result.NodeCount)
	}
}

func TestL3CRUD(t *testing.T) {
	engine := createTestEngine(t)

	// Create graph slot
	graphID := uint64(5001)
	slot := model.HypergraphSlot{
		IDHash:    graphID,
		Name:      "test graph",
		Source:    model.HypergraphSource{Kind: model.SourceManual},
		CreatedAt: 1000,
		UpdatedAt: 1000,
		Version:   1,
	}
	writeGraphSlot(engine, graphID, &slot)

	// Create node
	nodeID := uint64(5101)
	node := model.HypergraphNode{
		IDHash:     nodeID,
		GraphID:    graphID,
		Title:      "node1",
		NodeType:   "test",
		Content:    "content",
		Keywords:   []string{"key"},
		Importance: 0.5,
		CreatedAt:  1000,
		UpdatedAt:  1000,
		Version:    1,
	}
	data, _ := node.MarshalJSON()
	engine.WriteRecord(storage.RecL3GraphNode, nodeID, data)

	// Get L3
	hexID := hash.FormatHash(graphID)
	detail, err := GetL3(engine, hexID)
	if err != nil {
		t.Fatalf("get l3: %v", err)
	}
	if detail.Slot.Name != "test graph" {
		t.Errorf("unexpected name: %s", detail.Slot.Name)
	}
	if len(detail.Nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(detail.Nodes))
	}

	// Update L3
	newName := "renamed"
	updated, err := UpdateL3(engine, hexID, UpdateL3Fields{Name: &newName})
	if err != nil {
		t.Fatalf("update l3: %v", err)
	}
	if updated.Slot.Name != "renamed" {
		t.Errorf("update failed: %s", updated.Slot.Name)
	}

	// Delete L3
	if err := DeleteL3(engine, nil, hexID); err != nil {
		t.Fatalf("delete l3: %v", err)
	}
	_, err = GetL3(engine, hexID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestL5CRUD(t *testing.T) {
	engine := createTestEngine(t)

	chainID := uint64(6001)
	chain := model.ActionChainSlot{
		IDHash:      chainID,
		Title:       "deploy",
		Trigger:     "keyword deploy",
		Status:      model.ChainDraft,
		Confidence:  0.5,
		SuccessRate: 0.9,
		CreatedAt:   1000,
		UpdatedAt:   1000,
		Version:     1,
	}
	writeActionChain(engine, chainID, &chain)

	hexID := hash.FormatHash(chainID)
	got, err := GetL5(engine, hexID)
	if err != nil {
		t.Fatalf("get l5: %v", err)
	}
	if got.Title != "deploy" {
		t.Errorf("unexpected title: %s", got.Title)
	}

	newTitle := "deploy service"
	newStatus := "active"
	err = UpdateL5(engine, hexID, UpdateL5Fields{Title: &newTitle, Status: &newStatus})
	if err != nil {
		t.Fatalf("update l5: %v", err)
	}
	updated, _ := GetL5(engine, hexID)
	if updated.Title != "deploy service" {
		t.Errorf("title not updated: %s", updated.Title)
	}

	if err := DeleteL5(engine, hexID); err != nil {
		t.Fatalf("delete l5: %v", err)
	}
	_, err = GetL5(engine, hexID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestL4Archives(t *testing.T) {
	engine := createTestEngine(t)

	// Write 3 archives
	for i, content := range []string{"hello world", "rust code", "world news"} {
		arc := model.ArchiveSlot{
			IDHash:      uint64(7001 + i),
			ContentType: model.ContentText,
			Role:        0,
			ContextID:   1,
			CreatedAt:   int64(1000 + i*1000),
			Content:     content,
		}
		data, _ := json.Marshal(arc)
		engine.WriteRecord(storage.RecL4Archive, arc.IDHash, data)
	}

	// Query recent
	result, err := QueryArchives(engine, ArchiveQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("query archives: %v", err)
	}
	if result.Total != 3 {
		t.Errorf("expected 3 archives, got %d", result.Total)
	}

	// Query with keyword
	kw := "world"
	result, err = QueryArchives(engine, ArchiveQuery{Keyword: &kw, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("query archives with keyword: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("expected 2 archives matching 'world', got %d", result.Total)
	}
}
