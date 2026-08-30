// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"fmt"
	"path/filepath"
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
	ev := &core.TrajectorySlot{IDHash: common.HashID("ev:1"), SessionID: 9, Seq: 3, NodeType: core.NodeTypeEvent, PlanNodeRef: child.IDHash, EventType: "llm_request", Timestamp: 1000}
	_, _ = AppendTrajectory(engine, agentID, *ev)

	nodes := CollectPlanNodes(engine, agentID, 9)
	if len(nodes) != 2 {
		t.Fatalf("want 2 plan nodes, got %d", len(nodes))
	}
	events := CollectNodeEvents(engine, agentID, child.IDHash)
	if len(events) != 1 || events[0].EventType != "llm_request" {
		t.Fatalf("want 1 event llm_request, got %+v", events)
	}
}

func TestPlanNodeID_DoesNotCollideWithEventID(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "plan.meh"), 16)
	if err != nil {
		t.Fatal(err)
	}
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
