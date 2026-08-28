// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"testing"

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
