// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Acceptance item 2: the host may read L1, and Dream is the one that writes it. The layer
// had mechanics tests deep inside the packages but no case on the host's own surface, so
// nothing said out loud what a host actually sees: an empty listing before the first
// consolidation, one node per conversation after it, an edge between two conversations that
// talk about the same thing, and what the node's `topic_ids` promise — a snapshot of the
// last sync, not a live listing.

package test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
)

func TestInterfaceDreamBuildsTheAssociationLayer(t *testing.T) {
	llm := newMockLLM(t)
	db := newTestDB(t, openMockDB(t, filepath.Join(t.TempDir(), "l1.meh"), llm.srv.URL))

	if nodes, err := db.ListL1(); err != nil || len(nodes) != 0 {
		t.Fatalf("a domain that never consolidated answered %+v (err %v), want an empty list", nodes, err)
	}

	settle := func(scene bool, turns int) string {
		var sceneID string
		for i := 0; i < turns; i++ {
			res, err := db.Search(memhop.SearchQuery{NewScene: scene && i == 0})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			sceneID = res.Scene.SceneID
			if _, err := turn(db.Session, "rust 的所有权规则是什么", "靠移动语义保证内存安全"); err != nil {
				t.Fatalf("turn: %v", err)
			}
		}
		return sceneID
	}
	first := settle(true, 2)
	settle(true, 2)

	if _, err := db.Dream(context.Background(), ""); err != nil {
		t.Fatalf("Dream: %v", err)
	}
	nodes, err := db.ListL1()
	if err != nil {
		t.Fatalf("ListL1 after Dream: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("Dream built %+v, want one node per settled conversation", nodes)
	}
	byScene := map[string]memhop.SceneNodeView{}
	for _, n := range nodes {
		byScene[n.SceneID] = n
	}
	firstNode, ok := byScene[first]
	if !ok {
		t.Fatalf("no node names the first conversation, got %+v", nodes)
	}
	if len(firstNode.TopicIDs) != 2 {
		t.Fatalf("the node lists %+v, want the two turns its scene settled", firstNode.TopicIDs)
	}
	// A node synced minutes ago is at (near) full strength: the counter starts at 1 and the
	// only thing that moves it is decay, which the same pass already ran once. So the value
	// is just under 1, and nothing about it says "this trace is fading".
	if firstNode.Importance <= 0.99 || firstNode.Importance > 1.0 {
		t.Fatalf("a just-synced node reports importance %v, want 1 minus one small decay", firstNode.Importance)
	}
	// Both conversations settled the same keywords, so Dream should judge them related:
	// the edge is the whole reason the layer exists, and an id is readable only as "these
	// two nodes share it".
	if len(firstNode.EdgeIDs) == 0 {
		t.Fatalf("the first node names no edge: %+v", firstNode)
	}
	other := byScene[nodes[1].SceneID]
	if len(other.EdgeIDs) == 0 || other.EdgeIDs[0] != firstNode.EdgeIDs[0] {
		t.Fatalf("the two nodes do not share an edge: %+v vs %+v", firstNode, other)
	}

	// The snapshot claim: deleting a topic leaves the node's list as the last sync wrote
	// it, and the next pass rebuilds it.
	if err := db.DeleteTopic(firstNode.TopicIDs[0]); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	afterDelete, err := db.ListL1()
	if err != nil {
		t.Fatalf("ListL1 after delete: %v", err)
	}
	if got := nodeFor(t, afterDelete, first).TopicIDs; len(got) != 2 || got[0] != firstNode.TopicIDs[0] {
		t.Fatalf("the listing changed under the host's feet: %+v, want the last sync's snapshot", got)
	}
	if _, err := db.Dream(context.Background(), ""); err != nil {
		t.Fatalf("second Dream: %v", err)
	}
	third, err := db.ListL1()
	if err != nil {
		t.Fatalf("ListL1 after the second Dream: %v", err)
	}
	if got := nodeFor(t, third, first).TopicIDs; len(got) != 1 {
		t.Fatalf("the next pass did not rebuild the snapshot: %+v", got)
	}
	if len(third) != 2 {
		t.Fatalf("a second Dream minted extra nodes: %+v", third)
	}
}

func nodeFor(t *testing.T, nodes []memhop.SceneNodeView, sceneID string) memhop.SceneNodeView {
	t.Helper()
	for _, n := range nodes {
		if n.SceneID == sceneID {
			return n
		}
	}
	raw, _ := json.Marshal(nodes)
	t.Fatalf("no node for scene %s in %s", sceneID, raw)
	return memhop.SceneNodeView{}
}
