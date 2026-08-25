// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func tempEngine(t *testing.T) *core.StorageEngine {
	t.Helper()
	eng, err := core.Create(filepath.Join(t.TempDir(), "test.meh"), 128)
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { eng.Close(&core.IndexSnapshotData{}) })
	return eng
}

// TestSyncL1NodesFromL2 covers node creation, idempotent no-op, topic-set
// update and per-scene isolation.
func TestSyncL1NodesFromL2(t *testing.T) {
	engine := tempEngine(t)
	sceneA := common.FormatHash(common.HashID("sceneA"))

	if !CreateTopicL2(engine, sceneA, []string{"k1"}, 1000, 0) {
		t.Fatal("create topic 1")
	}
	if !CreateTopicL2(engine, sceneA, []string{"k2"}, 2000, 0) {
		t.Fatal("create topic 2")
	}
	changed, err := SyncL1NodesFromL2(engine)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if changed != 1 {
		t.Fatalf("want 1 node created, got %d", changed)
	}
	node, err := core.ReadSceneNode(engine, common.HashID("l1:"+sceneA))
	if err != nil {
		t.Fatal("l1 node missing")
	}
	if node.SceneID != mustParse(t, sceneA) || len(node.TopicIDs) != 2 {
		t.Fatalf("node mismatch: %+v", node)
	}
	if node.Importance != 1.0 {
		t.Fatalf("fresh node importance should be 1.0: %+v", node)
	}
	firstUpdatedAt := node.UpdatedAt

	// Unchanged topic set must be a no-op so decay keeps accumulating.
	changed, err = SyncL1NodesFromL2(engine)
	if err != nil {
		t.Fatalf("sync #2: %v", err)
	}
	if changed != 0 {
		t.Fatalf("want 0 changes, got %d", changed)
	}
	if node, err := core.ReadSceneNode(engine, common.HashID("l1:"+sceneA)); err == nil && node.UpdatedAt != firstUpdatedAt {
		t.Fatalf("no-op sync must not refresh UpdatedAt")
	}

	// A new topic in the scene updates the node in place.
	if !CreateTopicL2(engine, sceneA, []string{"k3"}, 3000, 0) {
		t.Fatal("create topic 3")
	}
	changed, err = SyncL1NodesFromL2(engine)
	if err != nil {
		t.Fatalf("sync #3: %v", err)
	}
	if changed != 1 {
		t.Fatalf("want 1 node updated, got %d", changed)
	}
	node, err = core.ReadSceneNode(engine, common.HashID("l1:"+sceneA))
	if err != nil {
		t.Fatalf("read node after update: %v", err)
	}
	if len(node.TopicIDs) != 3 || node.Importance != 1.0 {
		t.Fatalf("node should keep importance and grow topic set: %+v", node)
	}

	// A second scene gets its own node.
	sceneB := common.FormatHash(common.HashID("sceneB"))
	if !CreateTopicL2(engine, sceneB, []string{"kb"}, 1000, 0) {
		t.Fatal("create topic in scene B")
	}
	changed, err = SyncL1NodesFromL2(engine)
	if err != nil {
		t.Fatalf("sync #4: %v", err)
	}
	if changed != 1 {
		t.Fatalf("want 1 node for scene B, got %d", changed)
	}
	nodes := core.CollectAllSceneNodes(engine)
	if len(nodes) != 2 {
		t.Fatalf("want 2 nodes total, got %d", len(nodes))
	}
}

// TestSyncL1NodesFromL2SkipsCompressed verifies depth>2 topics do not enter
// nodes (compression groups are covered by their depth<=2 parent).
func TestSyncL1NodesFromL2SkipsCompressed(t *testing.T) {
	engine := tempEngine(t)
	sceneA := common.FormatHash(common.HashID("sceneA"))
	CreateTopicL2(engine, sceneA, []string{"k1"}, 1000, 0)

	parentID := core.ComputeTopicID(common.HashID(sceneA), 1000, 2000)
	deep := core.TopicSlot{
		ID: core.ComputeTopicID(common.HashID(sceneA), 1000, 2000), SceneID: common.HashID(sceneA),
		ParentID: &parentID, Depth: 3, UserKeywords: []string{"deep"},
		UserTimestamp: 1000, AgentTimestamp: 2000,
	}
	if err := core.WriteTopicSlot(engine, deep.ID, &deep); err != nil {
		t.Fatalf("write deep topic: %v", err)
	}
	if _, err := SyncL1NodesFromL2(engine); err != nil {
		t.Fatalf("sync: %v", err)
	}
	node, err := core.ReadSceneNode(engine, common.HashID("l1:"+sceneA))
	if err != nil || len(node.TopicIDs) != 1 {
		t.Fatalf("depth-3 topic must be excluded from the node: %+v", node)
	}
	if node.UpdatedAt > time.Now().UnixMilli() {
		t.Fatal("updated_at in the future")
	}
}

// TestBuildL1Hyperedges covers edge creation from keyword-overlap Jaccard,
// threshold filtering, idempotent refresh and weight strengthening (max wins).
func TestBuildL1Hyperedges(t *testing.T) {
	engine := tempEngine(t)
	sceneA := common.FormatHash(common.HashID("sceneA"))
	sceneB := common.FormatHash(common.HashID("sceneB"))
	sceneC := common.FormatHash(common.HashID("sceneC"))

	if !CreateTopicL2(engine, sceneA, []string{"memory", "agent"}, 1000, 0) {
		t.Fatal("create topic A1")
	}
	if !CreateTopicL2(engine, sceneB, []string{"memory", "database"}, 1000, 0) {
		t.Fatal("create topic B1")
	}
	if !CreateTopicL2(engine, sceneC, []string{"cooking", "food"}, 1000, 0) {
		t.Fatal("create topic C1")
	}
	if _, err := SyncL1NodesFromL2(engine); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// A-B share "memory" → Jaccard 1/3 ≈ 0.33 ≥ 0.15; A-C and B-C share nothing.
	n, err := BuildL1Hyperedges(engine, 0.15)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 edge, got %d", n)
	}
	nodeA, err := core.ReadSceneNode(engine, core.SceneNodeID(mustParse(t, sceneA)))
	if err != nil || len(nodeA.EdgeIDs) != 1 {
		t.Fatalf("node A should hold 1 edge: %+v err=%v", nodeA, err)
	}
	edge, err := core.ReadSceneEdge(engine, nodeA.EdgeIDs[0])
	if err != nil {
		t.Fatalf("read edge: %v", err)
	}
	if edge.Kind != core.HyperCoOccurrence || len(edge.NodeIDs) != 2 {
		t.Fatalf("edge kind/nodes mismatch: %+v", edge)
	}
	if math.Abs(float64(edge.Weight)-1.0/3.0) > 1e-4 {
		t.Fatalf("weight = %.4f, want 0.3333", edge.Weight)
	}

	// Idempotent: same overlap must not refresh (weight unchanged → no write).
	n, err = BuildL1Hyperedges(engine, 0.15)
	if err != nil || n != 0 {
		t.Fatalf("idempotent rebuild: n=%d err=%v", n, err)
	}

	// A higher threshold filters the weak edge out (nothing new created).
	n, err = BuildL1Hyperedges(engine, 0.5)
	if err != nil || n != 0 {
		t.Fatalf("threshold filter: n=%d err=%v", n, err)
	}

	// More shared terms strengthen the edge (max update wins).
	if !CreateTopicL2(engine, sceneA, []string{"database"}, 2000, 0) {
		t.Fatal("create topic A2")
	}
	if _, err := SyncL1NodesFromL2(engine); err != nil {
		t.Fatalf("sync #2: %v", err)
	}
	n, err = BuildL1Hyperedges(engine, 0.15)
	if err != nil || n != 1 {
		t.Fatalf("strengthen: n=%d err=%v", n, err)
	}
	edge, err = core.ReadSceneEdge(engine, nodeA.EdgeIDs[0])
	if err != nil {
		t.Fatalf("read edge after strengthen: %v", err)
	}
	if math.Abs(float64(edge.Weight)-2.0/3.0) > 1e-4 {
		t.Fatalf("weight = %.4f, want 0.6667", edge.Weight)
	}
}

func mustParse(t *testing.T, s string) uint64 {
	t.Helper()
	v, err := common.ParseID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}
