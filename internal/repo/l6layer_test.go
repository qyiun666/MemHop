// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestAppendReadTrajectoryOrdered(t *testing.T) {
	engine := tempEngine(t)
	if err := AppendTrajectory(engine, core.DefaultAgentID, core.TrajectorySlot{SessionID: 7, Seq: 2, EventType: "tool_call", Payload: "b", Timestamp: 200}); err != nil {
		t.Fatalf("append seq2: %v", err)
	}
	if err := AppendTrajectory(engine, core.DefaultAgentID, core.TrajectorySlot{SessionID: 7, Seq: 1, EventType: "turn_start", Payload: "a", Timestamp: 100}); err != nil {
		t.Fatalf("append seq1: %v", err)
	}
	if err := AppendTrajectory(engine, core.DefaultAgentID, core.TrajectorySlot{SessionID: 8, Seq: 1, EventType: "turn_start", Payload: "other", Timestamp: 100}); err != nil {
		t.Fatalf("append other: %v", err)
	}
	events, err := ReadTrajectory(engine, core.DefaultAgentID, 7)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 2 || events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("order mismatch: %+v", events)
	}
	if events[0].Payload != "a" || events[1].Payload != "b" {
		t.Fatalf("payload mismatch")
	}
}

func TestDeleteTrajectory(t *testing.T) {
	engine := tempEngine(t)
	for i := uint64(1); i <= 3; i++ {
		if err := AppendTrajectory(engine, core.DefaultAgentID, core.TrajectorySlot{SessionID: 5, Seq: i, EventType: "turn_start", Timestamp: int64(i)}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := DeleteTrajectory(engine, core.DefaultAgentID, 5); err != nil {
		t.Fatalf("delete: %v", err)
	}
	events, err := ReadTrajectory(engine, core.DefaultAgentID, 5)
	if err != nil {
		t.Fatalf("read after delete: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("want empty trajectory, got %d events", len(events))
	}
	if err := DeleteTrajectory(engine, core.DefaultAgentID, 999); err != nil {
		t.Fatalf("delete missing session: %v", err)
	}
}

func TestListTrajectorySessionsGroupsBySession(t *testing.T) {
	engine := tempEngine(t)
	append := func(sid, seq uint64, ts int64) {
		if err := AppendTrajectory(engine, core.DefaultAgentID, core.TrajectorySlot{SessionID: sid, Seq: seq, EventType: "turn_start", Timestamp: ts}); err != nil {
			t.Fatalf("append %d/%d: %v", sid, seq, err)
		}
	}
	append(7, 1, 100)
	append(7, 2, 250)
	append(8, 1, 300)

	list, err := ListTrajectorySessions(engine, core.DefaultAgentID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 sessions, got %+v", list)
	}
	if list[0].SessionID != common.FormatHash(7) || list[0].Steps != 2 || list[0].LastAppendAt != 250 {
		t.Fatalf("session 7 summary mismatch: %+v", list[0])
	}
	if list[1].SessionID != common.FormatHash(8) || list[1].Steps != 1 || list[1].LastAppendAt != 300 {
		t.Fatalf("session 8 summary mismatch: %+v", list[1])
	}
}

func TestListTrajectorySessionsEmptyDomain(t *testing.T) {
	engine := tempEngine(t)
	list, err := ListTrajectorySessions(engine, core.DefaultAgentID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("want 0 sessions, got %+v", list)
	}
}

func TestPruneTrajectoryBeforeDeletesOlderEvents(t *testing.T) {
	engine := tempEngine(t)
	append := func(sid, seq uint64, ts int64) {
		if err := AppendTrajectory(engine, core.DefaultAgentID, core.TrajectorySlot{SessionID: sid, Seq: seq, EventType: "turn_start", Timestamp: ts}); err != nil {
			t.Fatalf("append %d/%d: %v", sid, seq, err)
		}
	}
	append(5, 1, 100)
	append(5, 2, 200)
	append(6, 1, 300)

	n, err := PruneTrajectoryBefore(engine, core.DefaultAgentID, 200)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned = %d, want 1 (only ts<200)", n)
	}
	events, err := ReadTrajectory(engine, core.DefaultAgentID, 5)
	if err != nil || len(events) != 1 || events[0].Timestamp != 200 {
		t.Fatalf("session 5 after prune: %+v err=%v", events, err)
	}
	if other, err := ReadTrajectory(engine, core.DefaultAgentID, 6); err != nil || len(other) != 1 {
		t.Fatalf("session 6 must survive: %+v err=%v", other, err)
	}

	n, err = PruneTrajectoryBefore(engine, core.DefaultAgentID, 10_000)
	if err != nil || n != 2 {
		t.Fatalf("second prune = %d err=%v, want 2", n, err)
	}
	if left, err := ReadTrajectory(engine, core.DefaultAgentID, 6); err != nil || len(left) != 0 {
		t.Fatalf("session 6 should be empty: %+v err=%v", left, err)
	}
	if list, err := ListTrajectorySessions(engine, core.DefaultAgentID); err != nil || len(list) != 0 {
		t.Fatalf("post-prune list = %+v err=%v, want empty", list, err)
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
	got, err := GetCapabilityL5(engine, core.DefaultAgentID, common.FormatHash(cap.IDHash))
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
	got, err := GetCapabilityL5(engine, core.DefaultAgentID, common.FormatHash(cap.IDHash))
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
	got, err = GetCapabilityL5(engine, core.DefaultAgentID, common.FormatHash(cap.IDHash))
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
	if DeleteCapabilityL5(engine, core.DefaultAgentID, common.FormatHash(cap.IDHash)) == false {
		t.Fatalf("delete capability failed")
	}
	if got := core.CollectAllCapabilities(engine, core.DefaultAgentID); len(got) != 0 {
		t.Fatalf("want 0 capabilities, got %+v", got)
	}
	if DeleteCapabilityL5(engine, core.DefaultAgentID, common.FormatHash(cap.IDHash)) == false {
		t.Fatalf("second delete should stay idempotent")
	}
}
