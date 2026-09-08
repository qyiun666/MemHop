// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"fmt"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestAppendTrajectoryThenReadBack(t *testing.T) {
	engine := tempEngine(t)
	for _, ev := range []core.TrajectorySlot{
		{SessionID: 7, Seq: 1, EventType: "llm_request", Payload: "a", Timestamp: 100},
		{SessionID: 7, Seq: 2, EventType: "tool_call", Payload: "b", Timestamp: 200},
	} {
		if _, err := AppendTrajectory(engine, core.DefaultAgentID, ev); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	got := core.CollectAllTrajectories(engine, core.DefaultAgentID)
	if len(got) != 2 {
		t.Fatalf("want 2 events, got %+v", got)
	}
	bySeq := map[uint64]core.TrajectorySlot{}
	for _, ev := range got {
		if ev.SessionID != 7 {
			t.Fatalf("foreign session leaked: %+v", ev)
		}
		bySeq[ev.Seq] = ev
	}
	if bySeq[1].Payload != "a" || bySeq[2].Payload != "b" {
		t.Fatalf("payload mismatch: %+v", bySeq)
	}
	if bySeq[1].IDHash == 0 || bySeq[1].IDHash == bySeq[2].IDHash {
		t.Fatalf("id hashes must be set and distinct: %+v", bySeq)
	}

	n, err := DeleteTrajectoryByIDs(engine, core.DefaultAgentID, []uint64{bySeq[1].IDHash})
	if err != nil || n != 1 {
		t.Fatalf("delete by ids = %d err=%v, want 1", n, err)
	}
	if left := core.CollectAllTrajectories(engine, core.DefaultAgentID); len(left) != 1 || left[0].Seq != 2 {
		t.Fatalf("seq1 must be gone: %+v", left)
	}
}

func TestWritePlanNode_KeepsHashPlanNodeID(t *testing.T) {
	engine := tempEngine(t)
	agentID := core.DefaultAgentID
	id := core.HashPlanNode(9, "1.2.1")
	node := &core.TrajectorySlot{
		IDHash: id, SessionID: 9, Seq: 1, NodeType: core.NodeTypePlan,
		ParentID: 0, NodePath: "1.2.1", Status: core.StatusInProgress,
	}
	if _, err := WritePlanNode(engine, agentID, node); err != nil {
		t.Fatal(err)
	}
	got, err := core.ReadTrajectorySlot(engine, agentID, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.IDHash != id {
		t.Fatalf("id overwritten: want %d, got %d", id, got.IDHash)
	}
	if got.NodeType != core.NodeTypePlan {
		t.Fatalf("want NodeTypePlan, got %d", got.NodeType)
	}
}

func TestPlanAggregateCountsNodesAndEvents(t *testing.T) {
	engine := tempEngine(t)
	agentID := core.DefaultAgentID
	root := &core.TrajectorySlot{IDHash: core.HashPlanNode(9, "1"), SessionID: 9, Seq: 1, NodeType: core.NodeTypePlan, NodePath: "1", Status: core.StatusInProgress}
	child := &core.TrajectorySlot{IDHash: core.HashPlanNode(9, "1.1"), SessionID: 9, Seq: 2, NodeType: core.NodeTypePlan, NodePath: "1.1", Status: core.StatusDone}
	_, _ = WritePlanNode(engine, agentID, root)
	_, _ = WritePlanNode(engine, agentID, child)
	// 事件挂到 child 节点
	ev := &core.TrajectorySlot{IDHash: common.HashID("ev:1"), SessionID: 9, Seq: 3, NodeType: core.NodeTypeEvent, PlanNodeRef: child.IDHash, EventType: "llm_request", Timestamp: 1000}
	_, _ = AppendTrajectory(engine, agentID, *ev)

	aggs := CollectPlanAggregates(engine, agentID)
	if len(aggs) != 1 || len(aggs[0].Nodes) != 2 {
		t.Fatalf("want 1 plan of 2 nodes, got %+v", aggs)
	}
	if aggs[0].EventCount[child.IDHash] != 1 ||
		len(aggs[0].Events) != 1 || aggs[0].Events[0].EventType != "llm_request" {
		t.Fatalf("want 1 llm_request event bound to child, got %+v", aggs)
	}
}

func TestPlanNodeID_DoesNotCollideWithEventID(t *testing.T) {
	engine := tempEngine(t)
	agentID := core.DefaultAgentID
	// 同一组 (planID=9, nodePath="1") 与 (sessionID=9, seq=1)
	planNodeID := core.HashPlanNode(9, "1")
	evID := common.HashID(fmt.Sprintf("%d:%d", 9, 1))
	if planNodeID == evID {
		t.Fatalf("plan node id %d must not collide with event id %d", planNodeID, evID)
	}
	// 写节点 + 写事件到同一 agent，两者并存不覆盖
	node := &core.TrajectorySlot{IDHash: planNodeID, SessionID: 9, Seq: 1, NodeType: core.NodeTypePlan, NodePath: "1", Status: core.StatusInProgress}
	if _, err := WritePlanNode(engine, agentID, node); err != nil {
		t.Fatal(err)
	}
	ev := &core.TrajectorySlot{IDHash: evID, SessionID: 9, Seq: 1, NodeType: core.NodeTypeEvent, PlanNodeRef: planNodeID, EventType: "llm_request", Timestamp: 1000}
	if _, err := AppendTrajectory(engine, agentID, *ev); err != nil {
		t.Fatal(err)
	}
	nodeGot, err := core.ReadTrajectorySlot(engine, agentID, planNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if nodeGot.NodeType != core.NodeTypePlan {
		t.Fatalf("plan node overwritten by event: got %d", nodeGot.NodeType)
	}
	evGot, err := core.ReadTrajectorySlot(engine, agentID, evID)
	if err != nil {
		t.Fatal(err)
	}
	if evGot.NodeType != core.NodeTypeEvent {
		t.Fatalf("event overwritten: got %d", evGot.NodeType)
	}
}

func TestCollectPlanAggregatesGroupsPlans(t *testing.T) {
	engine := tempEngine(t)
	agentID := core.DefaultAgentID
	// plan 9: root "1" (pending) + child "1.1" (done), one event bound to each.
	root9 := &core.TrajectorySlot{IDHash: core.HashPlanNode(9, "1"), SessionID: 9, Seq: 1, NodeType: core.NodeTypePlan, NodePath: "1", Status: core.StatusPending, Timestamp: 100}
	child9 := &core.TrajectorySlot{IDHash: core.HashPlanNode(9, "1.1"), SessionID: 9, Seq: 2, NodeType: core.NodeTypePlan, NodePath: "1.1", Status: core.StatusDone, Timestamp: 200}
	// plan 3: single done root.
	root3 := &core.TrajectorySlot{IDHash: core.HashPlanNode(3, "1"), SessionID: 3, Seq: 1, NodeType: core.NodeTypePlan, NodePath: "1", Status: core.StatusDone, Timestamp: 50}
	for _, n := range []*core.TrajectorySlot{root9, child9, root3} {
		if _, err := WritePlanNode(engine, agentID, n); err != nil {
			t.Fatal(err)
		}
	}
	ev9a := core.TrajectorySlot{SessionID: 9, Seq: 1, NodeType: core.NodeTypeEvent, PlanNodeRef: root9.IDHash, EventType: "plan_step", Timestamp: 300}
	ev9b := core.TrajectorySlot{SessionID: 9, Seq: 2, NodeType: core.NodeTypeEvent, PlanNodeRef: root9.IDHash, EventType: "plan_step", Timestamp: 400}
	ev9c := core.TrajectorySlot{SessionID: 9, Seq: 3, NodeType: core.NodeTypeEvent, PlanNodeRef: child9.IDHash, EventType: "plan_step", Timestamp: 500}
	// A bare turn event references no node, so it must join no aggregate.
	bare := core.TrajectorySlot{SessionID: 5, Seq: 1, EventType: "llm_request", Timestamp: 900}
	for _, ev := range []core.TrajectorySlot{ev9a, ev9b, ev9c, bare} {
		if _, err := AppendTrajectory(engine, agentID, ev); err != nil {
			t.Fatal(err)
		}
	}

	aggs := CollectPlanAggregates(engine, agentID)
	if len(aggs) != 2 {
		t.Fatalf("want 2 plan aggregates, got %d", len(aggs))
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
	if p9.EventCount[root9.IDHash] != 2 || p9.EventCount[child9.IDHash] != 1 {
		t.Fatalf("plan9 event counts: %+v", p9.EventCount)
	}
	if len(p9.Events) != 3 || len(p3.Events) != 0 {
		t.Fatalf("event ids: plan9=%d plan3=%d", len(p9.Events), len(p3.Events))
	}
	if p9.CreatedAt != 100 || p9.LastActiveAt != 500 {
		t.Fatalf("plan9 window = [%d,%d], want [100,500]", p9.CreatedAt, p9.LastActiveAt)
	}
	if !p9.HasNonDone {
		t.Fatal("plan9 has a pending node, must be non-done")
	}
	if p3.HasNonDone {
		t.Fatal("plan3 is all-done")
	}
}
