// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 read surface of the composition root: the order it promises and the empty
// domain's answer. The values on a node are Dream's, so nothing here writes one
// — the nodes are placed directly to test the read.

package internal

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// The index underneath is a hash map, so insertion order says nothing about read
// order: without an explicit sort the same domain answers the same call twice in
// two different orders, and a host diffing two reads — or a client rendering one
// as JSON — sees churn that means nothing.
func TestListL1SortsByIDHash(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	// Deliberately scrambled, and none of them in sorted-id order.
	for _, scene := range []uint64{77, 3, 42} {
		id := core.SceneNodeID(scene)
		node := &core.SceneNode{IDHash: id, SceneID: scene, Importance: 1}
		if err := core.WriteSceneNode(db.engine, core.DefaultAgentID, id, node); err != nil {
			t.Fatalf("write node for scene %d: %v", scene, err)
		}
	}

	first, err := db.ListL1(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("want the three nodes written, got %d", len(first))
	}
	for i := 1; i < len(first); i++ {
		if first[i-1].IDHash > first[i].IDHash {
			t.Fatalf("nodes are not in id order at %d: %d then %d", i, first[i-1].IDHash, first[i].IDHash)
		}
	}

	again, err := db.ListL1(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("list again: %v", err)
	}
	for i := range first {
		if first[i].IDHash != again[i].IDHash {
			t.Fatalf("two reads of one domain disagree at %d: %d vs %d", i, first[i].IDHash, again[i].IDHash)
		}
	}
}

// A domain nobody has consolidated yet holds no nodes. The answer is an empty
// list rather than nil so the facade renders it as [] and a host can range over
// it without a nil check meaning two different things.
func TestListL1OnAnUndreamedDomainIsEmptyNotNil(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	nodes, err := db.ListL1(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if nodes == nil {
		t.Fatal("want an empty non-nil list, got nil")
	}
	if len(nodes) != 0 {
		t.Fatalf("an undreamed domain reported %d nodes", len(nodes))
	}
}

// One node the engine cannot return is reported, not left out: a host comparing
// two scenes' memory footprints cannot tell "Dream never built that node" apart
// from "this record is damaged" when the listing is just shorter.
func TestListL1ReportsUnreadableNode(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	damaged := core.SceneNodeID(7)
	for _, scene := range []uint64{7, 8} {
		id := core.SceneNodeID(scene)
		if err := core.WriteSceneNode(db.engine, core.DefaultAgentID, id, &core.SceneNode{
			IDHash: id, SceneID: scene, Importance: 1,
		}); err != nil {
			t.Fatalf("write node for scene %d: %v", scene, err)
		}
	}
	if _, err := db.engine.WriteRecord(core.DefaultAgentID, core.RecL1SceneNode, damaged, []byte(`{"id":`)); err != nil {
		t.Fatalf("make one node unreadable: %v", err)
	}
	if _, err := db.ListL1(core.DefaultAgentID); common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("the listing must report the node it cannot return, got %v", err)
	}
}
