// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 co-occurrence hyperedge capability tests.

package engram

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func tempEngine(t *testing.T) *core.StorageEngine {
	t.Helper()
	eng, err := core.Create(filepath.Join(t.TempDir(), "test.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	return eng
}

// mustCreateTopic writes one depth-1 turn topic under sceneID with the given
// keywords; hyperedge construction reads exactly that single keyword track.
func mustCreateTopic(t *testing.T, engine *core.StorageEngine, sceneID uint64, userTS int64, kws []string) {
	t.Helper()
	id := core.ComputeTurnTopicID(sceneID, uint64(userTS))
	if _, err := repo.CreateTurnTopicL2(engine, core.DefaultAgentID, sceneID, id, kws, userTS, userTS+1); err != nil {
		t.Fatalf("create topic %v under scene %s", kws, common.FormatHash(sceneID))
	}
}

// TestBuildHyperedges covers edge creation from keyword-overlap Jaccard,
// threshold filtering, idempotent re-measurement and weight strengthening over a
// node whose evidence moved.
func TestBuildHyperedges(t *testing.T) {
	engine := tempEngine(t)
	sceneA := common.HashID("sceneA")
	sceneB := common.HashID("sceneB")
	sceneC := common.HashID("sceneC")

	mustCreateTopic(t, engine, sceneA, 1000, []string{"memory", "agent"})
	mustCreateTopic(t, engine, sceneB, 1000, []string{"memory", "database"})
	mustCreateTopic(t, engine, sceneC, 1000, []string{"cooking", "food"})
	touched, err := repo.SyncL1NodesFromL2(engine, core.DefaultAgentID)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	// A-B share "memory" → Jaccard 1/3 ≈ 0.33 ≥ 0.15; A-C and B-C share nothing.
	n, err := BuildHyperedges(engine, core.DefaultAgentID, 0.15, touched)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 edge, got %d", n)
	}
	nodeA, err := core.ReadSceneNode(engine, core.DefaultAgentID, core.SceneNodeID(sceneA))
	if err != nil || len(nodeA.EdgeIDs) != 1 {
		t.Fatalf("node A should hold 1 edge: %+v err=%v", nodeA, err)
	}
	edge, err := core.ReadSceneEdge(engine, core.DefaultAgentID, nodeA.EdgeIDs[0])
	if err != nil {
		t.Fatalf("read edge: %v", err)
	}
	if len(edge.NodeIDs) != 2 {
		t.Fatalf("nodes mismatch: %+v", edge)
	}
	if math.Abs(float64(edge.Weight)-1.0/3.0) > 1e-4 {
		t.Fatalf("weight = %.4f, want 0.3333", edge.Weight)
	}

	// Idempotent: same overlap must not refresh (weight unchanged → no write).
	n, err = BuildHyperedges(engine, core.DefaultAgentID, 0.15, touched)
	if err != nil || n != 0 {
		t.Fatalf("idempotent rebuild: n=%d err=%v", n, err)
	}

	// A higher threshold filters the weak edge out (nothing new created).
	n, err = BuildHyperedges(engine, core.DefaultAgentID, 0.5, touched)
	if err != nil || n != 0 {
		t.Fatalf("threshold filter: n=%d err=%v", n, err)
	}

	// More shared terms strengthen the edge: scene A's evidence moved, so the
	// higher similarity is a new observation rather than a re-measurement.
	mustCreateTopic(t, engine, sceneA, 2000, []string{"database"})
	touched, err = repo.SyncL1NodesFromL2(engine, core.DefaultAgentID)
	if err != nil {
		t.Fatalf("sync #2: %v", err)
	}
	if len(touched) != 1 {
		t.Fatalf("only scene A's node moved, got %v", touched)
	}
	n, err = BuildHyperedges(engine, core.DefaultAgentID, 0.15, touched)
	if err != nil || n != 1 {
		t.Fatalf("strengthen: n=%d err=%v", n, err)
	}
	edge, err = core.ReadSceneEdge(engine, core.DefaultAgentID, nodeA.EdgeIDs[0])
	if err != nil {
		t.Fatalf("read edge after strengthen: %v", err)
	}
	if math.Abs(float64(edge.Weight)-2.0/3.0) > 1e-4 {
		t.Fatalf("weight = %.4f, want 0.6667", edge.Weight)
	}
}

// An edge that is there but will not read back is not an edge that is missing.
// Building a fresh one restarts CreatedAt, which is what the decay clock runs on,
// and writes the full similarity over a weight that had decayed away from it —
// the record of how weak that association had become is exactly what the rebuild
// throws away.
func TestBuildHyperedgesReportsUnreadableEdge(t *testing.T) {
	engine := tempEngine(t)
	sceneA, sceneB := common.HashID("sceneA"), common.HashID("sceneB")
	mustCreateTopic(t, engine, sceneA, 1000, []string{"memory", "agent"})
	mustCreateTopic(t, engine, sceneB, 1000, []string{"memory", "database"})
	if _, err := repo.SyncL1NodesFromL2(engine, core.DefaultAgentID); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := BuildHyperedges(engine, core.DefaultAgentID, 0.15, nil); err != nil {
		t.Fatalf("build: %v", err)
	}
	nodeA, err := core.ReadSceneNode(engine, core.DefaultAgentID, core.SceneNodeID(sceneA))
	if err != nil || len(nodeA.EdgeIDs) != 1 {
		t.Fatalf("node A should hold 1 edge: %+v err=%v", nodeA, err)
	}
	edgeID := nodeA.EdgeIDs[0]
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL1Hyperedge, edgeID, []byte(`{"id":`)); err != nil {
		t.Fatalf("make the edge unreadable: %v", err)
	}

	if _, err := BuildHyperedges(engine, core.DefaultAgentID, 0.15, nil); common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("the build must report the edge it could not read, got %v", err)
	}
	rt, data, err := engine.ReadRecord(core.DefaultAgentID, edgeID)
	if err != nil || rt != core.RecL1Hyperedge || string(data) != `{"id":` {
		t.Fatalf("the refused pass rewrote the edge anyway: rt=%d data=%q err=%v", rt, data, err)
	}
}

// The two scenes' keyword sets are the whole input to an edge's weight, so
// recomputing them over records that have not moved returns the similarity that
// edge was created from. Taking that as a strengthening — which is what an
// unconditional max did — put the weight back where decay had found it, every
// pass, forever: co-occurrence could not fade unless the scenes' vocabulary
// changed out from under it. A rise now needs one endpoint's evidence to have
// moved, and the same pass that says so gets the rise.
func TestBuildHyperedgesKeepsADecayedEdgeDecayed(t *testing.T) {
	engine := tempEngine(t)
	sceneA, sceneB := common.HashID("sceneA"), common.HashID("sceneB")
	mustCreateTopic(t, engine, sceneA, 1000, []string{"memory", "agent"})
	mustCreateTopic(t, engine, sceneB, 1000, []string{"memory", "database"})
	if _, err := repo.SyncL1NodesFromL2(engine, core.DefaultAgentID); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := BuildHyperedges(engine, core.DefaultAgentID, 0.15, nil); err != nil {
		t.Fatalf("build: %v", err)
	}
	nodeA, err := core.ReadSceneNode(engine, core.DefaultAgentID, core.SceneNodeID(sceneA))
	if err != nil || len(nodeA.EdgeIDs) != 1 {
		t.Fatalf("node A should hold 1 edge: %+v err=%v", nodeA, err)
	}
	edge, err := core.ReadSceneEdge(engine, core.DefaultAgentID, nodeA.EdgeIDs[0])
	if err != nil {
		t.Fatalf("read edge: %v", err)
	}
	full := edge.Weight
	edge.Weight = full / 2
	if err := core.WriteSceneEdge(engine, core.DefaultAgentID, edge.IDHash, edge); err != nil {
		t.Fatalf("age the edge: %v", err)
	}

	if n, err := BuildHyperedges(engine, core.DefaultAgentID, 0.15, nil); err != nil || n != 0 {
		t.Fatalf("a re-measurement with nothing moved must write nothing: n=%d err=%v", n, err)
	}
	aged, err := core.ReadSceneEdge(engine, core.DefaultAgentID, edge.IDHash)
	if err != nil {
		t.Fatalf("read the aged edge: %v", err)
	}
	if math.Abs(float64(aged.Weight)-float64(full)/2) > 1e-6 {
		t.Fatalf("decay was undone by re-reading the same keywords: weight %.4f, was %.4f", aged.Weight, full/2)
	}

	// One new turn in scene B moves the evidence the pair is measured over.
	mustCreateTopic(t, engine, sceneB, 2000, []string{"agent"})
	touched, err := repo.SyncL1NodesFromL2(engine, core.DefaultAgentID)
	if err != nil {
		t.Fatalf("sync #2: %v", err)
	}
	if n, err := BuildHyperedges(engine, core.DefaultAgentID, 0.15, touched); err != nil || n != 1 {
		t.Fatalf("changed evidence should strengthen the edge: n=%d err=%v", n, err)
	}
	risen, err := core.ReadSceneEdge(engine, core.DefaultAgentID, edge.IDHash)
	if err != nil {
		t.Fatalf("read the strengthened edge: %v", err)
	}
	if risen.Weight <= aged.Weight {
		t.Fatalf("the edge did not strengthen over new evidence: %.4f after %.4f", risen.Weight, aged.Weight)
	}
}
