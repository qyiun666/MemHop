// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestWritePlanNode_KeepsHashPlanNodeID(t *testing.T) {
	engine := tempEngine(t)
	agentID := core.DefaultAgentID
	id := core.HashPlanNode(9, "1.2.1")
	node := &core.PlanNode{
		IDHash: id, TopicID: 9, NodePath: "1.2.1",
		Status: core.StatusInProgress, UpdatedAt: 100,
	}
	if _, err := WritePlanNode(engine, agentID, node); err != nil {
		t.Fatal(err)
	}
	got, err := core.ReadPlanNode(engine, agentID, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.IDHash != id || got.NodePath != "1.2.1" {
		t.Fatalf("node identity lost: %+v", got)
	}
}

// A node's id is derived, so writing one whose id was built from a different
// topic or path is refused rather than silently landing somewhere else.
func TestWritePlanNodeRejectsAForeignID(t *testing.T) {
	engine := tempEngine(t)
	node := &core.PlanNode{
		IDHash: core.HashPlanNode(9, "2"), TopicID: 9, NodePath: "1",
		Status: core.StatusPending,
	}
	if _, err := WritePlanNode(engine, core.DefaultAgentID, node); err == nil {
		t.Fatal("an id that does not match the node's own topic/path must be refused")
	}
}

// A plan node and a content slot are addressed by the same topic id and nothing
// else, so the two derivations must stay apart — and the typed readers must keep
// a node from being overwritten by an event of the same number.
func TestPlanNodeAndContentCoexistUnderOneTopic(t *testing.T) {
	engine := tempEngine(t)
	agentID := core.DefaultAgentID
	nodeID := core.HashPlanNode(9, "1")
	contentID := core.HashContent(9, 1)
	if nodeID == contentID {
		t.Fatalf("plan node id %d must not collide with content id %d", nodeID, contentID)
	}
	node := &core.PlanNode{IDHash: nodeID, TopicID: 9, NodePath: "1", Status: core.StatusInProgress}
	if _, err := WritePlanNode(engine, agentID, node); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteArchiveSlot(engine, agentID, contentID, &core.ArchiveSlot{
		IDHash: contentID, Kind: core.KindEvent, Seq: 1, TopicID: 9, EventType: "llm_request",
	}); err != nil {
		t.Fatal(err)
	}
	gotNode, err := core.ReadPlanNode(engine, agentID, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if gotNode.Status != core.StatusInProgress {
		t.Fatalf("plan node overwritten by the content record: %+v", gotNode)
	}
	gotEv, err := core.ReadArchiveSlot(engine, agentID, contentID)
	if err != nil {
		t.Fatal(err)
	}
	if gotEv.Kind != core.KindEvent {
		t.Fatalf("content overwritten: %+v", gotEv)
	}
}

// An aggregate exists exactly while a topic owns at least one node: a turn's
// events no longer join it, and neither ordering nor the recency the retention
// exemption reads depends on them.
func TestCollectPlanNodesGroupsTrees(t *testing.T) {
	engine := tempEngine(t)
	agentID := core.DefaultAgentID
	nodes := []*core.PlanNode{
		{IDHash: core.HashPlanNode(9, "1"), TopicID: 9, NodePath: "1", Status: core.StatusPending, UpdatedAt: 100},
		{IDHash: core.HashPlanNode(9, "1.1"), TopicID: 9, NodePath: "1.1", Status: core.StatusDone, UpdatedAt: 500},
		{IDHash: core.HashPlanNode(3, "1"), TopicID: 3, NodePath: "1", Status: core.StatusDone, UpdatedAt: 50},
	}
	for _, n := range nodes {
		if _, err := WritePlanNode(engine, agentID, n); err != nil {
			t.Fatal(err)
		}
	}

	aggs := CollectPlanNodes(engine, agentID)
	if len(aggs) != 2 {
		t.Fatalf("want 2 plans, got %+v", aggs)
	}
	byTopic := map[uint64]PlanAggregate{}
	for _, a := range aggs {
		byTopic[a.TopicID] = a
	}
	p9, p3 := byTopic[9], byTopic[3]
	if len(p9.Nodes) != 2 || len(p3.Nodes) != 1 {
		t.Fatalf("node counts: plan9=%d plan3=%d", len(p9.Nodes), len(p3.Nodes))
	}
	if p9.Nodes[0].NodePath != "1" || p9.Nodes[1].NodePath != "1.1" {
		t.Fatalf("plan9 nodes must be nodePath-sorted: %+v", p9.Nodes)
	}
	if p9.LastActiveAt != 500 {
		t.Fatalf("plan9 LastActiveAt=%d want 500", p9.LastActiveAt)
	}
	if !p9.HasNonDone {
		t.Fatal("plan9 has a pending node, must be non-done")
	}
	if p3.HasNonDone {
		t.Fatal("plan3 is all-done")
	}
}

func TestDeletePlanNodesByTopicIDsTakesWholeTrees(t *testing.T) {
	engine := tempEngine(t)
	agentID := core.DefaultAgentID
	for _, n := range []*core.PlanNode{
		{IDHash: core.HashPlanNode(9, "1"), TopicID: 9, NodePath: "1"},
		{IDHash: core.HashPlanNode(9, "1.1"), TopicID: 9, NodePath: "1.1"},
		{IDHash: core.HashPlanNode(10, "1"), TopicID: 10, NodePath: "1"},
	} {
		if _, err := WritePlanNode(engine, agentID, n); err != nil {
			t.Fatal(err)
		}
	}
	n, err := DeletePlanNodesByTopicIDs(engine, agentID, []uint64{9})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("deleted %d nodes, want the two of topic 9", n)
	}
	left := CollectPlanNodes(engine, agentID)
	if len(left) != 1 || left[0].TopicID != 10 {
		t.Fatalf("another turn's tree must survive: %+v", left)
	}
	if n, err := DeletePlanNodesByTopicIDs(engine, agentID, nil); err != nil || n != 0 {
		t.Fatalf("no topics = no writes, got %d/%v", n, err)
	}
}

// CompareNodePath orders numerically, so a step list past nine does not fold
// "1.10" in front of "1.9".
func TestCompareNodePathIsNumericPerSegment(t *testing.T) {
	if CompareNodePath("1.10", "1.9") <= 0 {
		t.Fatal("1.10 must sort after 1.9")
	}
	if CompareNodePath("1", "1.1") >= 0 {
		t.Fatal("a parent must sort before its child")
	}
	if CompareNodePath("2", "1.9") <= 0 {
		t.Fatal("segment one decides first")
	}
}
