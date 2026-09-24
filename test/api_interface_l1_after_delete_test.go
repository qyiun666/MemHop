// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"context"
	"path/filepath"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
)

// Deleting a scene is the correction a host makes when it decides something should never have
// been remembered, and L1 is the layer whose whole content is a judgement about scenes — so it
// is where the question bites. What the measurement showed, and what this pins, is that the
// two halves of L1 behave differently:
//
//   - the deleted scene's own node is gone from `ListL1` with the delete itself;
//   - a surviving node keeps naming the co-occurrence edge that linked them until the next
//     consolidation. An edge record has no read of its own, but `SceneNodeView.edge_ids` is a
//     host-visible list of ids, so in that window the host is handed an id it cannot resolve.
//
// That gap is the deferred half, not a lost record — and it is worth stating rather than
// smoothing over, because a host that treats `edge_ids` as a live join key between two scenes
// will read a relationship that has already been withdrawn.
func TestInterfaceL1AfterDeletingOneOfTwoRelatedScenes(t *testing.T) {
	llm := newMockLLM(t)
	path := filepath.Join(t.TempDir(), "l1_after_delete.meh")
	m := openMockDB(t, path, llm.srv.URL, dreamOnlyWhenAsked)
	sess, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}

	var scenes []string
	for s := 0; s < 2; s++ {
		res, err := sess.Search(memhop.SearchQuery{NewScene: true})
		if err != nil {
			t.Fatalf("open scene %d: %v", s, err)
		}
		scenes = append(scenes, res.Scene.SceneID)
		for r := 0; r < 3; r++ {
			if _, err := sess.Search(memhop.SearchQuery{}); err != nil {
				t.Fatalf("open a turn: %v", err)
			}
			if _, err := turn(sess, "同一话题的提问", "同一话题的回答"); err != nil {
				t.Fatalf("close a turn: %v", err)
			}
		}
	}
	if _, err := sess.Dream(context.Background(), ""); err != nil {
		t.Fatalf("Dream: %v", err)
	}
	nodes, err := sess.ListL1()
	if err != nil {
		t.Fatalf("ListL1: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("two scenes settled one L1 node each, got %d: %+v", len(nodes), nodes)
	}
	related := 0
	for _, n := range nodes {
		if len(n.EdgeIDs) > 0 {
			related++
		}
	}
	if related != 2 {
		t.Fatalf("the two scenes are joined by %d of their nodes naming an edge, want both — without a join "+
			"there is nothing here to withdraw later", related)
	}

	doomed, survivor := scenes[0], scenes[1]
	if err := sess.DeleteScene(doomed); err != nil {
		t.Fatalf("DeleteScene: %v", err)
	}

	// The scene itself is unreachable right away, and so is its node.
	if _, err := sess.SceneContext(doomed); memhop.CodeOf(err) != memhop.ErrNotFound {
		t.Fatalf("the deleted scene still reads: %v", err)
	}
	nodes, err = sess.ListL1()
	if err != nil {
		t.Fatalf("ListL1 after the delete: %v", err)
	}
	if len(nodes) != 1 || nodes[0].SceneID != survivor {
		t.Fatalf("L1 still lists %d node(s) after one of two scenes was deleted: %+v", len(nodes), nodes)
	}
	// What the delete does not do is rewrite the survivor: it goes on naming the edge that
	// linked them. This is the deferred half, stated as measured rather than as hoped.
	if len(nodes[0].EdgeIDs) == 0 {
		t.Fatalf("the surviving node already lost its edge_ids — the premise of the next assertion is that "+
			"this window exists: %+v", nodes[0])
	}
	dangling := nodes[0].EdgeIDs[0]

	if _, err := sess.Dream(context.Background(), ""); err != nil {
		t.Fatalf("Dream after the delete: %v", err)
	}
	nodes, err = sess.ListL1()
	if err != nil {
		t.Fatalf("ListL1 after the consolidation: %v", err)
	}
	for _, id := range nodes[0].EdgeIDs {
		if id == dangling {
			t.Fatalf("the edge a deleted scene took away is still named by the survivor after a consolidation: %s", id)
		}
	}
}

// dreamOnlyWhenAsked keeps the passes in this file's own hands: the scene must not be
// consolidated behind the assertions, which would sink rows this test lists by hand.
func dreamOnlyWhenAsked(d *memhop.MemHopDefaults) {
	d.SceneDreamTopicThreshold = -1
	d.DreamCompressMinTopics = 100
}
