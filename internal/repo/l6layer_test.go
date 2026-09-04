// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"fmt"
	"slices"
	"strings"
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

func TestUpsertCapabilityL5PersistsDefinition(t *testing.T) {
	engine := tempEngine(t)
	cfg := `{"endpoint":"http://localhost:9000"}`
	cap := &core.Capability{
		Name: "整理代码", Type: core.CapabilityMCP, Summary: "整理代码",
		Trigger: "用户要求重构", Status: core.CapabilityActive,
		Origin: core.CapabilityOriginCrystallized,
		Resources: []core.ResourceRef{
			{Type: core.CapabilitySkill, Name: "deploy-checklist"},
			{Type: core.CapabilityMCP, Name: "deploy-mcp", Ref: "localhost:9000", Config: &cfg},
			{Type: core.CapabilityMCP, Name: "run_test"},
		},
	}
	existed, err := UpsertCapabilityL5(engine, core.DefaultAgentID, cap)
	if err != nil {
		t.Fatalf("create capability: %v", err)
	}
	if existed {
		t.Fatal("fresh capability should not exist yet")
	}
	got, err := GetCapabilityL5(engine, core.DefaultAgentID, cap.IDHash)
	if err != nil {
		t.Fatalf("get capability: %v", err)
	}
	if got.Type != core.CapabilityMCP || len(got.Resources) != 3 ||
		got.Resources[1].Name != "deploy-mcp" {
		t.Fatalf("resources mismatch: %+v", got)
	}
}

func TestUpsertCapabilityL5PreservesRuntimeFields(t *testing.T) {
	engine := tempEngine(t)
	cap := &core.Capability{
		Name: "t", Type: core.CapabilityMCP, Summary: "s", Trigger: "tr",
		Status: core.CapabilityActive, Origin: core.CapabilityOriginHost,
		Resources: []core.ResourceRef{{Type: core.CapabilityMCP, Name: "s1"}},
	}
	existed, err := UpsertCapabilityL5(engine, core.DefaultAgentID, cap)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if existed {
		t.Fatal("fresh capability should not exist yet")
	}
	got, err := GetCapabilityL5(engine, core.DefaultAgentID, cap.IDHash)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got.TriggerCount = 3
	got.SuccessRate = 0.75
	if err := core.WriteCapability(engine, core.DefaultAgentID, got.IDHash, got); err != nil {
		t.Fatalf("write runtime fields: %v", err)
	}

	cap2 := &core.Capability{
		Name: "t", Version: "2", Type: core.CapabilityComposite, Summary: "s2",
		Trigger: "tr2", Status: core.CapabilityDraft, Origin: core.CapabilityOriginHost,
		Resources: []core.ResourceRef{{Type: core.CapabilityMCP, Name: "tool"}},
		Workflow:  &core.Workflow{Steps: []core.WorkflowStep{{Ref: "tool"}}},
	}
	existed, err = UpsertCapabilityL5(engine, core.DefaultAgentID, cap2)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if existed == false {
		t.Fatal("expected existing capability")
	}
	got, err = GetCapabilityL5(engine, core.DefaultAgentID, cap.IDHash)
	if err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if got.TriggerCount != 3 || got.SuccessRate != 0.75 {
		t.Fatalf("runtime fields lost: %+v", got)
	}
	if got.Version != "2" || got.Type != core.CapabilityComposite || got.Status != core.CapabilityActive {
		t.Fatalf("definition not refreshed: %+v", got)
	}
}

func TestDeleteCapabilityL5(t *testing.T) {
	engine := tempEngine(t)
	cap := &core.Capability{Name: "t", Type: core.CapabilityMCP, Summary: "s", Trigger: "tr",
		Status: core.CapabilityDraft, Origin: core.CapabilityOriginHost,
		Resources: []core.ResourceRef{{Type: core.CapabilityMCP, Name: "s1"}}}
	if _, err := UpsertCapabilityL5(engine, core.DefaultAgentID, cap); err != nil {
		t.Fatalf("create capability: %v", err)
	}
	if DeleteCapabilityL5(engine, core.DefaultAgentID, cap.IDHash) == false {
		t.Fatalf("delete capability failed")
	}
	if got := core.CollectAllCapabilities(engine, core.DefaultAgentID); len(got) != 0 {
		t.Fatalf("want 0 capabilities, got %+v", got)
	}
	if DeleteCapabilityL5(engine, core.DefaultAgentID, cap.IDHash) == false {
		t.Fatalf("second delete should stay idempotent")
	}
}

func TestWritePlanNode_KeepsHashPlanNodeID(t *testing.T) {
	engine := tempEngine(t)
	agentID := core.DefaultAgentID
	id := core.HashPlanNode(9, "1.2.1")
	node := &core.TrajectorySlot{
		IDHash: id, SessionID: 9, Seq: 1, NodeType: core.NodeTypePlan,
		PlanID: 9, ParentID: 0, NodePath: "1.2.1", Status: core.StatusInProgress,
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

func TestCollectPlanNodesAndNodeEvents(t *testing.T) {
	engine := tempEngine(t)
	agentID := core.DefaultAgentID
	root := &core.TrajectorySlot{IDHash: core.HashPlanNode(9, "1"), SessionID: 9, Seq: 1, NodeType: core.NodeTypePlan, PlanID: 9, NodePath: "1", Status: core.StatusInProgress}
	child := &core.TrajectorySlot{IDHash: core.HashPlanNode(9, "1.1"), SessionID: 9, Seq: 2, NodeType: core.NodeTypePlan, PlanID: 9, NodePath: "1.1", Status: core.StatusDone}
	_, _ = WritePlanNode(engine, agentID, root)
	_, _ = WritePlanNode(engine, agentID, child)
	// 事件挂到 child 节点
	ev := &core.TrajectorySlot{IDHash: common.HashID("ev:1"), SessionID: 9, Seq: 3, NodeType: core.NodeTypeEvent, PlanID: 9, PlanNodeRef: child.IDHash, EventType: "llm_request", Timestamp: 1000}
	_, _ = AppendTrajectory(engine, agentID, *ev)

	nodes := CollectPlanNodes(engine, agentID, 9)
	if len(nodes) != 2 {
		t.Fatalf("want 2 plan nodes, got %d", len(nodes))
	}
	aggs := CollectPlanAggregates(engine, agentID)
	if len(aggs) != 1 || aggs[0].EventCount[child.IDHash] != 1 ||
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
	node := &core.TrajectorySlot{IDHash: planNodeID, SessionID: 9, Seq: 1, NodeType: core.NodeTypePlan, PlanID: 9, NodePath: "1", Status: core.StatusInProgress}
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
	root9 := &core.TrajectorySlot{IDHash: core.HashPlanNode(9, "1"), SessionID: 9, Seq: 1, NodeType: core.NodeTypePlan, PlanID: 9, NodePath: "1", Status: core.StatusPending, Timestamp: 100}
	child9 := &core.TrajectorySlot{IDHash: core.HashPlanNode(9, "1.1"), SessionID: 9, Seq: 2, NodeType: core.NodeTypePlan, PlanID: 9, NodePath: "1.1", Status: core.StatusDone, Timestamp: 200}
	// plan 3: single done root.
	root3 := &core.TrajectorySlot{IDHash: core.HashPlanNode(3, "1"), SessionID: 3, Seq: 1, NodeType: core.NodeTypePlan, PlanID: 3, NodePath: "1", Status: core.StatusDone, Timestamp: 50}
	for _, n := range []*core.TrajectorySlot{root9, child9, root3} {
		if _, err := WritePlanNode(engine, agentID, n); err != nil {
			t.Fatal(err)
		}
	}
	ev9a := core.TrajectorySlot{SessionID: 9, Seq: 1, NodeType: core.NodeTypeEvent, PlanID: 9, PlanNodeRef: root9.IDHash, EventType: "plan_step", Timestamp: 300}
	ev9b := core.TrajectorySlot{SessionID: 9, Seq: 2, NodeType: core.NodeTypeEvent, PlanID: 9, PlanNodeRef: root9.IDHash, EventType: "plan_step", Timestamp: 400}
	ev9c := core.TrajectorySlot{SessionID: 9, Seq: 3, NodeType: core.NodeTypeEvent, PlanID: 9, PlanNodeRef: child9.IDHash, EventType: "plan_step", Timestamp: 500}
	// A bare turn event (PlanID=0) must not leak into any aggregate.
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
	byPlan := map[uint64]PlanAggregate{}
	for _, a := range aggs {
		byPlan[a.PlanID] = a
	}
	p9, p3 := byPlan[9], byPlan[3]
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

func TestDeletePlanRecordsRemovesNodesAndEvents(t *testing.T) {
	engine := tempEngine(t)
	agentID := core.DefaultAgentID
	root9 := &core.TrajectorySlot{IDHash: core.HashPlanNode(9, "1"), SessionID: 9, Seq: 1, NodeType: core.NodeTypePlan, PlanID: 9, NodePath: "1", Status: core.StatusPending, Timestamp: 100}
	if _, err := WritePlanNode(engine, agentID, root9); err != nil {
		t.Fatal(err)
	}
	root3 := &core.TrajectorySlot{IDHash: core.HashPlanNode(3, "1"), SessionID: 3, Seq: 1, NodeType: core.NodeTypePlan, PlanID: 3, NodePath: "1", Status: core.StatusDone, Timestamp: 50}
	if _, err := WritePlanNode(engine, agentID, root3); err != nil {
		t.Fatal(err)
	}
	ev9 := core.TrajectorySlot{SessionID: 9, Seq: 1, NodeType: core.NodeTypeEvent, PlanID: 9, PlanNodeRef: root9.IDHash, EventType: "plan_step", Timestamp: 300}
	bare := core.TrajectorySlot{SessionID: 5, Seq: 1, EventType: "llm_request", Timestamp: 900}
	for _, ev := range []core.TrajectorySlot{ev9, bare} {
		if _, err := AppendTrajectory(engine, agentID, ev); err != nil {
			t.Fatal(err)
		}
	}

	n, err := DeletePlanRecords(engine, agentID, 9)
	if err != nil || n != 2 {
		t.Fatalf("delete plan 9 = %d err=%v, want 2 (1 node + 1 event)", n, err)
	}
	left := core.CollectAllTrajectories(engine, agentID)
	if len(left) != 2 {
		t.Fatalf("want plan3 node + bare event left, got %d", len(left))
	}
	for _, ev := range left {
		if ev.PlanID == 9 {
			t.Fatalf("plan 9 record survived: %+v", ev)
		}
	}
	// Idempotent: deleting again (or an unknown plan) removes nothing.
	if n, err := DeletePlanRecords(engine, agentID, 9); err != nil || n != 0 {
		t.Fatalf("second delete = %d err=%v, want 0", n, err)
	}
}

// mustEventRef returns the id of the single event bound to a plan node.
func mustEventRef(t *testing.T, engine *core.StorageEngine, agentID, nodeRef uint64) uint64 {
	t.Helper()
	var found uint64
	for _, ev := range core.CollectAllTrajectories(engine, agentID) {
		if ev.NodeType == core.NodeTypeEvent && ev.PlanNodeRef == nodeRef {
			found = ev.IDHash
		}
	}
	if found == 0 {
		t.Fatalf("no event bound to node %d", nodeRef)
	}
	return found
}

func TestDeletePlanNodeBranchCascades(t *testing.T) {
	engine := tempEngine(t)
	agentID := core.DefaultAgentID
	planID := uint64(9)
	mkNode := func(nodePath string, parent uint64) uint64 {
		id := core.HashPlanNode(planID, nodePath)
		node := &core.TrajectorySlot{
			IDHash: id, SessionID: planID, Seq: uint64(len(strings.Split(nodePath, "."))),
			NodeType: core.NodeTypePlan, PlanID: planID, ParentID: parent,
			NodePath: nodePath, Status: core.StatusPending, Timestamp: 100,
		}
		if _, err := WritePlanNode(engine, agentID, node); err != nil {
			t.Fatal(err)
		}
		return id
	}
	root := mkNode("1", 0)
	c1 := mkNode("1.1", root)
	mkNode("1.2", root)
	mkNode("2", 0) // a sibling root outside the "1" branch
	// Bind a real event to "1.1" so the cascade is observable on disk.
	if _, err := AppendTrajectory(engine, agentID, core.TrajectorySlot{
		SessionID: planID, Seq: 1, NodeType: core.NodeTypeEvent, PlanID: planID,
		PlanNodeRef: c1, EventType: "llm_request", Payload: "x", Timestamp: 200,
	}); err != nil {
		t.Fatal(err)
	}
	bound := mustEventRef(t, engine, agentID, c1)
	deleted, err := DeletePlanNodeBranch(engine, agentID, planID, "1")
	if err != nil || len(deleted) != 4 {
		t.Fatalf("delete branch \"1\" = %d ids err=%v, want 4 (3 nodes + 1 bound event)", len(deleted), err)
	}
	if !slices.Contains(deleted, bound) {
		t.Fatalf("the cascade must report the event id it deleted, got %v", deleted)
	}
	nodes := CollectPlanNodes(engine, agentID, planID)
	if len(nodes) != 1 || nodes[0].NodePath != "2" {
		t.Fatalf("surviving nodes = %+v, want only sibling root \"2\"", nodes)
	}
	for _, ev := range core.CollectAllTrajectories(engine, agentID) {
		if ev.PlanNodeRef == c1 {
			t.Fatalf("bound event must cascade with its pruned node: %+v", ev)
		}
	}
	// Idempotent on a vanished path.
	if again, err := DeletePlanNodeBranch(engine, agentID, planID, "1"); err != nil || len(again) != 0 {
		t.Fatalf("second delete = %d ids err=%v, want none", len(again), err)
	}
	// Prefix boundaries: "1.1" must not be matched by a sibling like "1.10".
	mkNode("1.1", 0)
	mkNode("1.10", 0)
	exact, err := DeletePlanNodeBranch(engine, agentID, planID, "1.1")
	if err != nil || len(exact) != 1 {
		t.Fatalf("delete \"1.1\" = %d ids err=%v, want 1 (exact node only)", len(exact), err)
	}
	if got := CollectPlanNodes(engine, agentID, planID); len(got) != 2 || got[0].NodePath != "2" || got[1].NodePath != "1.10" {
		t.Fatalf("prefix sibling must survive: %+v", got)
	}
}
