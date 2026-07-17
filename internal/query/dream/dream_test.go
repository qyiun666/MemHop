// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"memhop/internal/common/config"
	"memhop/internal/core/index"
	"memhop/internal/core/model"
	"memhop/internal/core/storage"
	"memhop/internal/common/hash"
	"memhop/internal/common/timeutil"
)

// ============================================================================
// Mock LLM Provider
// ============================================================================

// MockLlmProvider returns a pre-configured ConsolidationOutput.
type MockLlmProvider struct {
	Output *ConsolidationOutput
	Err    error
}

func (m *MockLlmProvider) Consolidate(_ *ConsolidationInput) (*ConsolidationOutput, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Output, nil
}

// ============================================================================
// Test helpers
// ============================================================================

func createTestEngine(t *testing.T) *storage.StorageEngine {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.meh")
	engine, err := storage.Create(path, 768)
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	return engine
}

func writeTestTopic(t *testing.T, engine *storage.StorageEngine, topic *model.TopicSlot) {
	t.Helper()
	data, err := json.Marshal(topic)
	if err != nil {
		t.Fatalf("marshal topic: %v", err)
	}
	_, err = engine.WriteRecord(storage.RecL2Topic, topic.ID, data)
	if err != nil {
		t.Fatalf("write topic: %v", err)
	}
}

func writeTestSceneNode(t *testing.T, engine *storage.StorageEngine, node *model.SceneNode) {
	t.Helper()
	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal scene node: %v", err)
	}
	_, err = engine.WriteRecord(storage.RecL1SceneNode, node.IDHash, data)
	if err != nil {
		t.Fatalf("write scene node: %v", err)
	}
}

func writeTestSceneEdge(t *testing.T, engine *storage.StorageEngine, edge *model.SceneEdge) {
	t.Helper()
	data, err := json.Marshal(edge)
	if err != nil {
		t.Fatalf("marshal scene edge: %v", err)
	}
	_, err = engine.WriteRecord(storage.RecL1Hyperedge, edge.IDHash, data)
	if err != nil {
		t.Fatalf("write scene edge: %v", err)
	}
}

func writeTestProfile(t *testing.T, engine *storage.StorageEngine, profile *model.ProfileSlot) {
	t.Helper()
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	_, err = engine.WriteRecord(storage.RecL0Profile, profile.IDHash, data)
	if err != nil {
		t.Fatalf("write profile: %v", err)
	}
}

func readTestTopic(t *testing.T, engine *storage.StorageEngine, id uint64) *model.TopicSlot {
	t.Helper()
	topic, err := readTopic(engine, id)
	if err != nil {
		t.Fatalf("read topic: %v", err)
	}
	return topic
}

func readTestSceneNode(t *testing.T, engine *storage.StorageEngine, id uint64) *model.SceneNode {
	t.Helper()
	node := readSceneNode(engine, id)
	if node == nil {
		t.Fatalf("scene node not found: %d", id)
	}
	return node
}

// ============================================================================
// Tests
// ============================================================================

func TestL2Compress(t *testing.T) {
	engine := createTestEngine(t)
	sparseIdx := index.NewSparseIndex()
	l2Meta := index.NewL2MetaIndex()

	nowMs := timeutil.NowMs()
	sceneID := uint64(100)

	// Create 3 depth-1 nodes
	ids := []uint64{hash.HashID("node1"), hash.HashID("node2"), hash.HashID("node3")}
	for i, id := range ids {
		topic := &model.TopicSlot{
			ID: id, SceneID: sceneID, Depth: 1,
			UserKeywords:  []string{"keyword" + string(rune('A'+i))},
			AgentKeywords: []string{"agent" + string(rune('A'+i))},
			CreatedAt:     nowMs, UpdatedAt: nowMs, Version: 1,
		}
		writeTestTopic(t, engine, topic)
		l2Meta.Update(metaFromTopic(id, topic))
		sparseIdx.AddDocument(id, topic.UserKeywords, uint32(len(topic.UserKeywords)))
	}

	groups := []L2Group{{
		SceneID:       sceneID,
		NodeHashes:    ids,
		MergedTitle:   "Merged Topic",
		MergedSummary: "Summary of merged topics",
	}}

	result, err := ApplyL2Groups(groups, engine, sparseIdx, l2Meta, nil)
	if err != nil {
		t.Fatalf("ApplyL2Groups: %v", err)
	}

	if result.GroupsDetected != 1 {
		t.Errorf("GroupsDetected = %d, want 1", result.GroupsDetected)
	}
	if result.NodesMerged != 3 {
		t.Errorf("NodesMerged = %d, want 3", result.NodesMerged)
	}
	if result.ParentsCreated != 1 {
		t.Errorf("ParentsCreated = %d, want 1", result.ParentsCreated)
	}
	if result.NodesSunk != 3 {
		t.Errorf("NodesSunk = %d, want 3", result.NodesSunk)
	}

	// Children should now be depth 2
	for _, id := range ids {
		topic := readTestTopic(t, engine, id)
		if topic.Depth != 2 {
			t.Errorf("child %d depth = %d, want 2", id, topic.Depth)
		}
	}
}

func TestL1Decay(t *testing.T) {
	engine := createTestEngine(t)
	sparseIdx := index.NewSparseIndex()
	l2Meta := index.NewL2MetaIndex()
	nowMs := timeutil.NowMs()

	// Old node: 400 hours ago → will be removed (importance decays below threshold)
	oldTime := nowMs - 400*3_600_000
	node1 := &model.SceneNode{
		IDHash: 1, SceneID: 1000, TopicIDs: []uint64{1000},
		Depth: 1, Importance: 0.5, CreatedAt: oldTime, UpdatedAt: oldTime,
	}
	writeTestSceneNode(t, engine, node1)

	// Recent node: stays
	node2 := &model.SceneNode{
		IDHash: 2, SceneID: 1000, TopicIDs: []uint64{1001},
		Depth: 1, Importance: 0.9, CreatedAt: nowMs, UpdatedAt: nowMs,
	}
	writeTestSceneNode(t, engine, node2)

	cfg := &DecayParams{
		LambdaNode:             0.01,
		LambdaEdge:             0.02,
		NodeRemoveThreshold:    0.05,
		NodePruneEdgeThreshold: 0.15,
		EdgeRemoveThreshold:    0.05,
		MinEdgeNodes:           2,
	}

	report, err := DecayL1Network(engine, cfg, l2Meta, sparseIdx)
	if err != nil {
		t.Fatalf("DecayL1Network: %v", err)
	}

	if report.RemovedNodes != 1 {
		t.Errorf("RemovedNodes = %d, want 1", report.RemovedNodes)
	}
	if report.DecayedNodes != 1 {
		t.Errorf("DecayedNodes = %d, want 1", report.DecayedNodes)
	}
	if engine.Contains(1) {
		t.Error("old node should be removed")
	}
	if !engine.Contains(2) {
		t.Error("recent node should survive")
	}
}

func TestL1DecayPruneEdges(t *testing.T) {
	engine := createTestEngine(t)
	sparseIdx := index.NewSparseIndex()
	l2Meta := index.NewL2MetaIndex()
	nowMs := timeutil.NowMs()
	oldTime := nowMs - 20*3_600_000

	cfg := &DecayParams{
		LambdaNode:             0.01,
		LambdaEdge:             0.02,
		NodeRemoveThreshold:    0.05,
		NodePruneEdgeThreshold: 0.15,
		EdgeRemoveThreshold:    0.05,
		MinEdgeNodes:           2,
	}

	// Node with importance that decays between remove and prune thresholds
	target := (cfg.NodeRemoveThreshold + cfg.NodePruneEdgeThreshold) / 2.0
	startImp := target / float32(math.Exp(-cfg.LambdaNode*20.0))

	node := &model.SceneNode{
		IDHash: 100, SceneID: 1, TopicIDs: []uint64{1},
		Depth: 1, Importance: startImp,
		CreatedAt: oldTime, UpdatedAt: oldTime,
		EdgeIDs: []uint64{50, 51},
	}
	writeTestSceneNode(t, engine, node)

	report, err := DecayL1Network(engine, cfg, l2Meta, sparseIdx)
	if err != nil {
		t.Fatalf("DecayL1Network: %v", err)
	}
	if report.PrunedEdges != 2 {
		t.Errorf("PrunedEdges = %d, want 2", report.PrunedEdges)
	}
	if report.DecayedNodes != 1 {
		t.Errorf("DecayedNodes = %d, want 1", report.DecayedNodes)
	}
	// Node should survive but with cleared edges
	n := readTestSceneNode(t, engine, 100)
	if len(n.EdgeIDs) != 0 {
		t.Errorf("node edges should be cleared, got %d", len(n.EdgeIDs))
	}
}

// TestL1EdgeDecayNotCompounding verifies that repeated decay runs accumulate
// as exp(-λ·totalElapsed), not by re-applying the full age on each run.
func TestL1EdgeDecayNotCompounding(t *testing.T) {
	engine := createTestEngine(t)
	t0 := timeutil.NowMs() - 100*3_600_000

	cfg := &DecayParams{
		LambdaEdge:          0.02,
		EdgeRemoveThreshold: 0.0001,
		MinEdgeNodes:        2,
	}

	// Old record without LastDecayAt: first decay counts from CreatedAt.
	writeTestSceneEdge(t, engine, &model.SceneEdge{
		IDHash: 10, NodeIDs: []uint64{1, 2},
		Weight: 1.0, CreatedAt: t0,
	})
	report := &L1DecayReport{}

	// First decay run at t0+10h.
	edge := readSceneEdge(engine, 10)
	if edge == nil {
		t.Fatal("edge should exist")
	}
	if err := decayOneEdge(engine, cfg, edge, 10, nil, t0+10*3_600_000, report); err != nil {
		t.Fatalf("first decayOneEdge: %v", err)
	}
	edge = readSceneEdge(engine, 10)
	wantFirst := float32(math.Exp(-cfg.LambdaEdge * 10.0))
	if math.Abs(float64(edge.Weight-wantFirst)) > 1e-5 {
		t.Errorf("after first decay weight = %v, want %v", edge.Weight, wantFirst)
	}
	if edge.LastDecayAt != t0+10*3_600_000 {
		t.Errorf("LastDecayAt = %d, want %d", edge.LastDecayAt, t0+10*3_600_000)
	}

	// Second decay run at t0+25h: only the 15h increment may be applied.
	if err := decayOneEdge(engine, cfg, edge, 10, nil, t0+25*3_600_000, report); err != nil {
		t.Fatalf("second decayOneEdge: %v", err)
	}
	edge = readSceneEdge(engine, 10)
	want := float32(math.Exp(-cfg.LambdaEdge * 25.0))
	if math.Abs(float64(edge.Weight-want)) > 1e-5 {
		t.Errorf("weight = %v, want %v (exp(-λ·total), not compounded)", edge.Weight, want)
	}
	if edge.LastDecayAt != t0+25*3_600_000 {
		t.Errorf("LastDecayAt = %d, want %d", edge.LastDecayAt, t0+25*3_600_000)
	}
}

// TestRebuildL1RemovesStaleNodeFromEdges verifies that removing a stale node
// also cleans the node's reference from its edges' NodeIDs.
func TestRebuildL1RemovesStaleNodeFromEdges(t *testing.T) {
	engine := createTestEngine(t)
	sparseIdx := index.NewSparseIndex()
	l2Meta := index.NewL2MetaIndex()
	nowMs := timeutil.NowMs()
	cfg := &DecayParams{MinEdgeNodes: 2}

	// Stale node: its L2 topic (999) no longer exists in the engine.
	writeTestSceneNode(t, engine, &model.SceneNode{
		IDHash: 100, SceneID: 1, TopicIDs: []uint64{999},
		Depth: 1, Importance: 0.9, CreatedAt: nowMs, UpdatedAt: nowMs,
		EdgeIDs: []uint64{500, 501},
	})

	// Live node: topic 300 exists and has no deep meta.
	writeTestTopic(t, engine, &model.TopicSlot{
		ID: 300, SceneID: 1, Depth: 1, CreatedAt: nowMs, UpdatedAt: nowMs, Version: 1,
	})
	writeTestSceneNode(t, engine, &model.SceneNode{
		IDHash: 200, SceneID: 1, TopicIDs: []uint64{300},
		Depth: 1, Importance: 0.9, CreatedAt: nowMs, UpdatedAt: nowMs,
		EdgeIDs: []uint64{500, 501},
	})

	// Edge 500 keeps enough members after the stale node is removed.
	writeTestSceneEdge(t, engine, &model.SceneEdge{
		IDHash: 500, NodeIDs: []uint64{100, 200, 201}, Weight: 0.8, CreatedAt: nowMs,
	})
	// Edge 501 drops below MinEdgeNodes and must be deleted.
	writeTestSceneEdge(t, engine, &model.SceneEdge{
		IDHash: 501, NodeIDs: []uint64{100, 200}, Weight: 0.8, CreatedAt: nowMs,
	})

	if _, err := RebuildL1FromL2(engine, sparseIdx, l2Meta, cfg); err != nil {
		t.Fatalf("RebuildL1FromL2: %v", err)
	}

	if engine.Contains(100) {
		t.Error("stale node should be removed")
	}
	edge := readSceneEdge(engine, 500)
	if edge == nil {
		t.Fatal("edge 500 should survive")
	}
	if len(edge.NodeIDs) != 2 {
		t.Errorf("edge 500 NodeIDs = %v, want 2 members", edge.NodeIDs)
	}
	for _, n := range edge.NodeIDs {
		if n == 100 {
			t.Errorf("edge 500 NodeIDs still references removed node: %v", edge.NodeIDs)
		}
	}
	if engine.Contains(501) {
		t.Error("edge 501 should be removed (below MinEdgeNodes)")
	}
	live := readTestSceneNode(t, engine, 200)
	for _, e := range live.EdgeIDs {
		if e == 501 {
			t.Errorf("live node EdgeIDs still references removed edge: %v", live.EdgeIDs)
		}
	}
}

// TestL1DecayRemovesNodeFromSparseIndex verifies that decay-removed nodes are
// also removed from the BM25 sparse index.
func TestL1DecayRemovesNodeFromSparseIndex(t *testing.T) {
	engine := createTestEngine(t)
	sparseIdx := index.NewSparseIndex()
	l2Meta := index.NewL2MetaIndex()
	nowMs := timeutil.NowMs()

	oldTime := nowMs - 400*3_600_000
	writeTestSceneNode(t, engine, &model.SceneNode{
		IDHash: 1, SceneID: 1000, TopicIDs: []uint64{1000},
		Depth: 1, Importance: 0.5, CreatedAt: oldTime, UpdatedAt: oldTime,
	})
	writeTestSceneNode(t, engine, &model.SceneNode{
		IDHash: 2, SceneID: 1000, TopicIDs: []uint64{1001},
		Depth: 1, Importance: 0.9, CreatedAt: nowMs, UpdatedAt: nowMs,
	})
	sparseIdx.AddDocument(1, []string{"uniqueterm"}, 1)
	sparseIdx.AddDocument(2, []string{"uniqueterm"}, 1)

	cfg := &DecayParams{
		LambdaNode:             0.01,
		LambdaEdge:             0.02,
		NodeRemoveThreshold:    0.05,
		NodePruneEdgeThreshold: 0.15,
		EdgeRemoveThreshold:    0.05,
		MinEdgeNodes:           2,
	}
	report, err := DecayL1Network(engine, cfg, l2Meta, sparseIdx)
	if err != nil {
		t.Fatalf("DecayL1Network: %v", err)
	}
	if report.RemovedNodes != 1 {
		t.Fatalf("RemovedNodes = %d, want 1", report.RemovedNodes)
	}

	foundSurvivor := false
	for _, doc := range sparseIdx.Search([]string{"uniqueterm"}, 10) {
		if doc.IDHash == 1 {
			t.Error("BM25 should not return the decay-removed node")
		}
		if doc.IDHash == 2 {
			foundSurvivor = true
		}
	}
	if !foundSurvivor {
		t.Error("BM25 should still return the surviving node")
	}
}

func TestL0Form(t *testing.T) {
	engine := createTestEngine(t)
	sparseIdx := index.NewSparseIndex()

	// Add some documents with high-frequency terms
	for i := 0; i < 5; i++ {
		sparseIdx.AddDocument(uint64(i), []string{"golang", "rust", "python"}, 3)
	}
	for i := 5; i < 8; i++ {
		sparseIdx.AddDocument(uint64(i), []string{"golang", "docker"}, 2)
	}

	err := GenerateProfile(engine, sparseIdx)
	if err != nil {
		t.Fatalf("GenerateProfile: %v", err)
	}

	profileID := hash.HashID("profile")
	if !engine.Contains(profileID) {
		t.Fatal("profile should exist")
	}

	rt, data, err := engine.ReadRecord(profileID)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if rt != storage.RecL0Profile {
		t.Fatalf("record type = %d, want %d", rt, storage.RecL0Profile)
	}

	var profile model.ProfileSlot
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}

	if profile.Name != "Agent" {
		t.Errorf("Name = %q, want %q", profile.Name, "Agent")
	}
	// "golang" should be in top keywords (appears in 8 docs)
	if profile.Preferences["top_keywords"] == "" {
		t.Error("top_keywords should not be empty")
	}
}

func TestL0FormUpdate(t *testing.T) {
	engine := createTestEngine(t)
	sparseIdx := index.NewSparseIndex()

	// Create existing profile with lexicon
	profileID := hash.HashID("profile")
	profile := &model.ProfileSlot{
		IDHash:      profileID,
		Name:        "TestAgent",
		Role:        "assistant",
		Personality: "old",
		Lexicon:     map[string]string{"hello": "greeting"},
		CreatedAt:   timeutil.NowMs(),
		UpdatedAt:   timeutil.NowMs(),
		Version:     1,
	}
	writeTestProfile(t, engine, profile)

	sparseIdx.AddDocument(1, []string{"rust", "golang"}, 2)

	err := GenerateProfile(engine, sparseIdx)
	if err != nil {
		t.Fatalf("GenerateProfile: %v", err)
	}

	updated := readTestProfile(t, engine, profileID)
	// Name should be preserved
	if updated.Name != "TestAgent" {
		t.Errorf("Name = %q, want %q", updated.Name, "TestAgent")
	}
	// Lexicon should be preserved
	if updated.Lexicon["hello"] != "greeting" {
		t.Error("lexicon should be preserved")
	}
	// Personality should be updated
	if updated.Personality == "old" {
		t.Error("personality should be updated")
	}
}

func readTestProfile(t *testing.T, engine *storage.StorageEngine, id uint64) *model.ProfileSlot {
	t.Helper()
	_, data, err := engine.ReadRecord(id)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	var p model.ProfileSlot
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	return &p
}

func TestPipelineStageFailure(t *testing.T) {
	engine := createTestEngine(t)
	sparseIdx := index.NewSparseIndex()
	l2Meta := index.NewL2MetaIndex()

	// Create a profile so habit stage doesn't fail
	profileID := hash.HashID("profile")
	profile := &model.ProfileSlot{
		IDHash: profileID, Name: "Agent", Role: "assistant",
		Lexicon:         make(map[string]string),
		Preferences:     make(map[string]string),
		EmotionPatterns: make(map[string]string),
		CreatedAt:       timeutil.NowMs(), UpdatedAt: timeutil.NowMs(), Version: 1,
	}
	writeTestProfile(t, engine, profile)

	mockLLM := &MockLlmProvider{
		Output: &ConsolidationOutput{
			L2Groups:      NewEmptySection[[]L2Group](),
			L3Extractions: NewEmptySection[[]L3Extraction](),
			Habits:        NewEmptySection[HabitAnalysis](),
			Crystals:      NewEmptySection[[]CrystalDef](),
		},
	}

	decayCfg := &config.DecayConfig{
		LambdaNode:              0.01,
		LambdaEdge:              0.02,
		NodeRemoveThreshold:     0.05,
		NodePruneEdgesThreshold: 0.15,
		EdgeRemoveThreshold:     0.05,
		MinEdgeNodes:            2,
	}

	report, err := DreamPipeline(engine, sparseIdx, mockLLM, nil, decayCfg, l2Meta, nil)
	if err != nil {
		t.Fatalf("DreamPipeline: %v", err)
	}

	// All stages should succeed
	for _, s := range report.Stages {
		if s.Status == "failed" {
			t.Errorf("stage %q should not have failed: %s", s.Name, s.Error)
		}
	}
}

func TestDreamPipelineEndToEnd(t *testing.T) {
	engine := createTestEngine(t)
	sparseIdx := index.NewSparseIndex()
	l2Meta := index.NewL2MetaIndex()
	nowMs := timeutil.NowMs()

	// Create profile for habit stage
	profileID := hash.HashID("profile")
	profile := &model.ProfileSlot{
		IDHash: profileID, Name: "Agent", Role: "assistant",
		Lexicon:         make(map[string]string),
		StyleTraits:     []string{},
		EmotionPatterns: make(map[string]string),
		Preferences:     make(map[string]string),
		CreatedAt:       nowMs, UpdatedAt: nowMs, Version: 1,
	}
	writeTestProfile(t, engine, profile)

	// Create 2 depth-1 L2 topics in same scene
	sceneID := uint64(42)
	id1 := hash.HashID("e2e_node1")
	id2 := hash.HashID("e2e_node2")
	for _, id := range []uint64{id1, id2} {
		topic := &model.TopicSlot{
			ID: id, SceneID: sceneID, Depth: 1,
			UserKeywords: []string{"test"}, CreatedAt: nowMs, UpdatedAt: nowMs, Version: 1,
		}
		writeTestTopic(t, engine, topic)
		l2Meta.Update(metaFromTopic(id, topic))
	}

	mockLLM := &MockLlmProvider{
		Output: &ConsolidationOutput{
			L2Groups: NewValidSection([]L2Group{{
				SceneID:       sceneID,
				NodeHashes:    []uint64{id1, id2},
				MergedTitle:   "Test Merge",
				MergedSummary: "Merged summary for end-to-end test",
			}}),
			L3Extractions: NewValidSection([]L3Extraction{{
				ContextID: id1,
				Concepts: []LlmConcept{
					{Name: "Go", NodeType: "language", Description: "Go programming language"},
				},
				Relations: []LlmRelation{},
			}}),
			Habits: NewValidSection(HabitAnalysis{
				Lexicon:         map[string]string{"golang": "Go language"},
				StyleTraits:     []string{"technical"},
				EmotionPatterns: map[string]string{},
			}),
			Crystals: NewValidSection([]CrystalDef{{
				Condition:  "user_asks == true",
				Action:     "provide_answer",
				Steps:      []CrystalStep{{Action: "search", Parameters: nil}},
				Confidence: 0.8,
			}}),
		},
	}

	decayCfg := &config.DecayConfig{
		LambdaNode:              0.01,
		LambdaEdge:              0.02,
		NodeRemoveThreshold:     0.05,
		NodePruneEdgesThreshold: 0.15,
		EdgeRemoveThreshold:     0.05,
		MinEdgeNodes:            2,
	}

	report, err := DreamPipeline(engine, sparseIdx, mockLLM, []uint64{id1, id2}, decayCfg, l2Meta, nil)
	if err != nil {
		t.Fatalf("DreamPipeline: %v", err)
	}

	// Check stage count (l2_compress, l1_rebuild, l1_decay, l0_profile)
	if len(report.Stages) < 4 {
		t.Errorf("Stages count = %d, want >= 4", len(report.Stages))
	}

	// All stages should succeed
	for _, s := range report.Stages {
		if s.Status != "success" {
			t.Errorf("stage %q status = %q, want success (error: %s)", s.Name, s.Status, s.Error)
		}
	}
}

func TestDreamReport(t *testing.T) {
	engine := createTestEngine(t)
	sparseIdx := index.NewSparseIndex()
	l2Meta := index.NewL2MetaIndex()

	// Create profile
	profileID := hash.HashID("profile")
	profile := &model.ProfileSlot{
		IDHash: profileID, Name: "Agent", Role: "assistant",
		Lexicon:         make(map[string]string),
		StyleTraits:     []string{},
		EmotionPatterns: make(map[string]string),
		Preferences:     make(map[string]string),
		CreatedAt:       timeutil.NowMs(), UpdatedAt: timeutil.NowMs(), Version: 1,
	}
	writeTestProfile(t, engine, profile)

	mockLLM := &MockLlmProvider{
		Output: &ConsolidationOutput{
			L2Groups:      NewEmptySection[[]L2Group](),
			L3Extractions: NewEmptySection[[]L3Extraction](),
			Habits:        NewEmptySection[HabitAnalysis](),
			Crystals:      NewEmptySection[[]CrystalDef](),
		},
	}

	decayCfg := &config.DecayConfig{
		LambdaNode:              0.01,
		LambdaEdge:              0.02,
		NodeRemoveThreshold:     0.05,
		NodePruneEdgesThreshold: 0.15,
		EdgeRemoveThreshold:     0.05,
		MinEdgeNodes:            2,
	}

	report, err := DreamPipeline(engine, sparseIdx, mockLLM, nil, decayCfg, l2Meta, nil)
	if err != nil {
		t.Fatalf("DreamPipeline: %v", err)
	}

	// Verify report fields
	if report.ConsolidatedCount != 0 {
		t.Errorf("ConsolidatedCount = %d, want 0", report.ConsolidatedCount)
	}

	// Check that all expected stages are present
	stageNames := make(map[string]bool)
	for _, s := range report.Stages {
		stageNames[s.Name] = true
	}
	// Check that non-LLM stages are always present
	expectedStages := []string{
		"l1_rebuild", "l1_decay", "l0_profile",
	}
	for _, name := range expectedStages {
		if !stageNames[name] {
			t.Errorf("missing stage: %s", name)
		}
	}
	// LLM-dependent stages are skipped when sections are Empty
	skippedStages := []string{"l2_compress"}
	for _, name := range skippedStages {
		if stageNames[name] {
			t.Errorf("stage %q should be skipped for empty sections", name)
		}
	}
}

func TestEmotionalBoost(t *testing.T) {
	base := 1.0
	boosted := ApplyEmotionalBoost(base, 0.8, 0.7)
	if boosted >= base {
		t.Errorf("high emotion should reduce lambda: got %f, base %f", boosted, base)
	}

	lowBoost := ApplyEmotionalBoost(base, 0.1, 0.1)
	if math.Abs(lowBoost-base) >= 0.1 {
		t.Errorf("low emotion should minimally affect lambda: got %f", lowBoost)
	}
}

func TestSectionHelpers(t *testing.T) {
	valid := NewValidSection([]string{"a", "b"})
	if !valid.IsValid() {
		t.Error("valid section should be valid")
	}
	if valid.NeedsRetry() {
		t.Error("valid section should not need retry")
	}

	empty := NewEmptySection[string]()
	if !empty.IsValid() {
		t.Error("empty section should be valid")
	}

	failed := NewFailedSection[string]("parse error")
	if failed.IsValid() {
		t.Error("failed section should not be valid")
	}
	if !failed.NeedsRetry() {
		t.Error("failed section should need retry")
	}
}

func TestPruneLowQualityCrystals(t *testing.T) {
	engine := createTestEngine(t)
	nowMs := timeutil.NowMs()

	// Low quality chain: should be pruned
	lowChain := &model.ActionChainSlot{
		IDHash: 1001, Title: "low", Trigger: "test",
		Status: model.ChainDraft, Confidence: 0.2, TriggerCount: 2,
		CreatedAt: nowMs, UpdatedAt: nowMs, Version: 1,
	}
	data, _ := json.Marshal(lowChain)
	_, _ = engine.WriteRecord(storage.RecL5ActionChain, 1001, data)

	// High quality chain: should survive
	highChain := &model.ActionChainSlot{
		IDHash: 1002, Title: "high", Trigger: "test",
		Status: model.ChainDraft, Confidence: 0.9, TriggerCount: 10,
		CreatedAt: nowMs, UpdatedAt: nowMs, Version: 1,
	}
	data, _ = json.Marshal(highChain)
	_, _ = engine.WriteRecord(storage.RecL5ActionChain, 1002, data)

	pruned, err := PruneLowQualityCrystals(engine)
	if err != nil {
		t.Fatalf("PruneLowQualityCrystals: %v", err)
	}
	if len(pruned) != 1 {
		t.Errorf("pruned count = %d, want 1", len(pruned))
	}
	if engine.Contains(1001) {
		t.Error("low quality chain should be pruned")
	}
	if !engine.Contains(1002) {
		t.Error("high quality chain should survive")
	}
}

// suppress unused import
var _ = os.DevNull
