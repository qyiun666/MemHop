// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 co-occurrence hyperedge capability tests.

package engram

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

func TestMain(m *testing.M) {
	if err := index.InitTokenizer(index.EngineAuto); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

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

// TestBuildHyperedges covers edge creation from keyword-overlap Jaccard,
// threshold filtering, idempotent refresh and weight strengthening (max wins).
func TestBuildHyperedges(t *testing.T) {
	engine := tempEngine(t)
	sceneA := common.FormatHash(common.HashID("sceneA"))
	sceneB := common.FormatHash(common.HashID("sceneB"))
	sceneC := common.FormatHash(common.HashID("sceneC"))

	if !repo.CreateTopicL2(engine, core.DefaultAgentID, sceneA, []string{"memory", "agent"}, 1000, 0) {
		t.Fatal("create topic A1")
	}
	if !repo.CreateTopicL2(engine, core.DefaultAgentID, sceneB, []string{"memory", "database"}, 1000, 0) {
		t.Fatal("create topic B1")
	}
	if !repo.CreateTopicL2(engine, core.DefaultAgentID, sceneC, []string{"cooking", "food"}, 1000, 0) {
		t.Fatal("create topic C1")
	}
	if _, err := repo.SyncL1NodesFromL2(engine, core.DefaultAgentID); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// A-B share "memory" → Jaccard 1/3 ≈ 0.33 ≥ 0.15; A-C and B-C share nothing.
	n, err := BuildHyperedges(engine, core.DefaultAgentID, 0.15)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 edge, got %d", n)
	}
	nodeA, err := core.ReadSceneNode(engine, core.DefaultAgentID, core.SceneNodeID(mustParse(t, sceneA)))
	if err != nil || len(nodeA.EdgeIDs) != 1 {
		t.Fatalf("node A should hold 1 edge: %+v err=%v", nodeA, err)
	}
	edge, err := core.ReadSceneEdge(engine, core.DefaultAgentID, nodeA.EdgeIDs[0])
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
	n, err = BuildHyperedges(engine, core.DefaultAgentID, 0.15)
	if err != nil || n != 0 {
		t.Fatalf("idempotent rebuild: n=%d err=%v", n, err)
	}

	// A higher threshold filters the weak edge out (nothing new created).
	n, err = BuildHyperedges(engine, core.DefaultAgentID, 0.5)
	if err != nil || n != 0 {
		t.Fatalf("threshold filter: n=%d err=%v", n, err)
	}

	// More shared terms strengthen the edge (max update wins).
	if !repo.CreateTopicL2(engine, core.DefaultAgentID, sceneA, []string{"database"}, 2000, 0) {
		t.Fatal("create topic A2")
	}
	if _, err := repo.SyncL1NodesFromL2(engine, core.DefaultAgentID); err != nil {
		t.Fatalf("sync #2: %v", err)
	}
	n, err = BuildHyperedges(engine, core.DefaultAgentID, 0.15)
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

func mustParse(t *testing.T, s string) uint64 {
	t.Helper()
	v, err := common.ParseID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}
