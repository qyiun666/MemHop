// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package scenefind

import (
	"math"
	"testing"

	"github.com/qyiun666/MemHop/internal/cap/engram"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// TestSpreadingActivation covers one-hop and two-hop activation, start-scene
// exclusion, threshold pruning and the no-node/isolated empty results.
func TestSpreadingActivation(t *testing.T) {
	engine := newTestEngine(t)
	sceneA := common.HashID("sceneA")
	sceneB := common.HashID("sceneB")
	sceneC := common.HashID("sceneC")
	sceneD := common.HashID("sceneD") // isolated: node, no edges

	mk := func(scene uint64, kws []string) {
		t.Helper()
		if !repo.CreateTopicL2(engine, core.DefaultAgentID, scene, kws, 1000, 0) {
			t.Fatal("create topic")
		}
	}
	mk(sceneA, []string{"memory", "agent"})
	mk(sceneB, []string{"memory", "database"})
	mk(sceneC, []string{"database", "code"})
	mk(sceneD, []string{"cooking", "food"})
	if _, err := repo.SyncL1NodesFromL2(engine, core.DefaultAgentID); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := engram.BuildHyperedges(engine, core.DefaultAgentID, 0.15); err != nil {
		t.Fatalf("build edges: %v", err)
	}
	l2Meta := index.BuildL2MetaFromEngine(engine, core.DefaultAgentID)
	sceneBHash := sceneB
	sceneDHash := sceneD

	// A-B share "memory" (J=1/3): activation = 1×1/3×0.5 ≈ 0.1667. The
	// two-hop B→C path yields 0.1667×1/3×0.5 ≈ 0.0278 < l1ActivationThreshold
	// (0.05) → pruned by the default walk limits.
	hits := SpreadingActivation(core.DefaultAgentID, engine, l2Meta, sceneA)
	if len(hits) != 1 || hits[0].SceneID != sceneBHash {
		t.Fatalf("want only scene B, got %+v", hits)
	}
	if math.Abs(float64(hits[0].Score)-1.0/6.0) > 1e-4 {
		t.Fatalf("activation = %.4f, want 0.1667", hits[0].Score)
	}
	if len(hits[0].Topics) != 1 {
		t.Fatalf("want 1 topic on B, got %d", len(hits[0].Topics))
	}

	// A scene without an L1 node (created after the last Dream) → empty.
	sceneFresh := common.HashID("sceneFresh")
	if hits := SpreadingActivation(core.DefaultAgentID, engine, l2Meta, sceneFresh); len(hits) != 0 {
		t.Fatalf("fresh scene must have no associations, got %+v", hits)
	}

	// An isolated scene has a node but no edges → empty.
	if hits := SpreadingActivation(core.DefaultAgentID, engine, l2Meta, sceneDHash); len(hits) != 0 {
		t.Fatalf("isolated scene must have no associations, got %+v", hits)
	}
}
