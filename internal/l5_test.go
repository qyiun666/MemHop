// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func writeCapability(t *testing.T, engine *core.StorageEngine, c *core.Capability) {
	t.Helper()
	if err := core.WriteCapability(engine, core.DefaultAgentID, c.IDHash, c); err != nil {
		t.Fatalf("write capability: %v", err)
	}
}

func TestListCapabilities(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	c1 := core.Capability{IDHash: common.HashID("c1"), Name: "修复编译错误", Type: core.CapabilityMCP, Status: core.CapabilityActive, UpdatedAt: 3000}
	c2 := core.Capability{IDHash: common.HashID("c2"), Name: "代码审查流程", Type: core.CapabilitySkill, Status: core.CapabilityDraft, UpdatedAt: 1000}
	c3 := core.Capability{IDHash: common.HashID("c3"), Name: "发布版本", Type: core.CapabilityComposite, Status: core.CapabilityActive, UpdatedAt: 2000}
	writeCapability(t, engine, &c1)
	writeCapability(t, engine, &c2)
	writeCapability(t, engine, &c3)

	out, err := db.ListCapabilities(CapabilityListQuery{})
	if err != nil {
		t.Fatalf("ListCapabilities: %v", err)
	}
	if len(out) != 3 || out[0].IDHash != c1.IDHash || out[1].IDHash != c3.IDHash || out[2].IDHash != c2.IDHash {
		t.Fatalf("all: want [c1 c3 c2], got %v", idsOfCapabilities(out))
	}

	active := core.CapabilityActive
	out, err = db.ListCapabilities(CapabilityListQuery{Status: &active})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("status active: want 2, got %d", len(out))
	}

	typ := core.CapabilityComposite
	out, err = db.ListCapabilities(CapabilityListQuery{Type: &typ})
	if err != nil {
		t.Fatalf("type: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != c3.IDHash {
		t.Fatalf("type composite: want [c3], got %v", idsOfCapabilities(out))
	}

	out, err = db.ListCapabilities(CapabilityListQuery{Keyword: "编译"})
	if err != nil {
		t.Fatalf("keyword: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != c1.IDHash {
		t.Fatalf("keyword: want [c1], got %v", idsOfCapabilities(out))
	}
}

func TestListCapabilitiesEmpty(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	out, err := db.ListCapabilities(CapabilityListQuery{})
	if err != nil {
		t.Fatalf("ListCapabilities: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("want 0 capabilities, got %d", len(out))
	}
}

func TestActivateCapability(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	cap := &core.Capability{Name: "待激活", Type: core.CapabilityMCP, Status: core.CapabilityDraft, IDHash: core.CapabilityID("待激活")}
	writeCapability(t, db.engine, cap)
	id := common.FormatHash(cap.IDHash)

	got, err := db.ActivateCapability(id)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if got.Status != core.CapabilityActive {
		t.Fatalf("status = %v, want active", got.Status)
	}
	if got.UpdatedAt == 0 {
		t.Fatalf("UpdatedAt not refreshed: %+v", got)
	}
	// Re-activating an active capability is idempotent.
	if _, err := db.ActivateCapability(id); err != nil {
		t.Fatalf("re-activate: %v", err)
	}
	// Unknown IDs surface ErrNotFound instead of inventing a record.
	if _, err := db.ActivateCapability(common.FormatHash(common.HashID("missing"))); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("missing id: want ErrNotFound, got %v", err)
	}
}

func TestRecordCapabilityUsage(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	cap := &core.Capability{Name: "用量能力", Type: core.CapabilityMCP, Status: core.CapabilityActive, IDHash: core.CapabilityID("用量能力")}
	writeCapability(t, db.engine, cap)
	id := common.FormatHash(cap.IDHash)

	got, err := db.RecordCapabilityUsage(id, true)
	if err != nil {
		t.Fatalf("first usage: %v", err)
	}
	if got.TriggerCount != 1 || got.SuccessRate != 1.0 {
		t.Fatalf("first success: %+v", got)
	}

	got, err = db.RecordCapabilityUsage(id, false)
	if err != nil {
		t.Fatalf("second usage: %v", err)
	}
	if got.TriggerCount != 2 || got.SuccessRate != 0.5 {
		t.Fatalf("second failure: %+v", got)
	}

	if _, err := db.RecordCapabilityUsage(common.FormatHash(common.HashID("missing")), true); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("missing id: want ErrNotFound, got %v", err)
	}
}

func TestUpdateCapability(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	cap := &core.Capability{Name: "可更新", Type: core.CapabilityMCP, Status: core.CapabilityActive, IDHash: core.CapabilityID("可更新")}
	writeCapability(t, db.engine, cap)
	id := common.FormatHash(cap.IDHash)

	summary := "新的摘要"
	trigger := "新触发词"
	typ := core.CapabilityComposite
	resources := []core.ResourceRef{
		{Type: core.CapabilityMCP, Name: "m1"},
		{Type: core.CapabilitySkill, Name: "s1"},
	}
	workflow := &core.Workflow{Steps: []core.WorkflowStep{{Ref: "m1", Action: "do"}, {Ref: "s1"}}}

	got, err := db.UpdateCapability(id, CapabilityPatch{
		Summary: &summary, Trigger: &trigger, Type: &typ, Resources: &resources, Workflow: workflow,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Summary != summary || got.Trigger != trigger || got.Type != typ {
		t.Fatalf("fields not applied: %+v", got)
	}
	if len(got.Resources) != 2 || got.Workflow == nil || len(got.Workflow.Steps) != 2 {
		t.Fatalf("resources/workflow not applied: %+v", got)
	}
	// The updated record is no longer byte-identical to an import.
	if got.FileHash != "" {
		t.Fatalf("FileHash must be cleared after update, got %q", got.FileHash)
	}

	// Partial update leaves untouched fields unchanged.
	version := "2.0"
	got, err = db.UpdateCapability(id, CapabilityPatch{Version: &version})
	if err != nil {
		t.Fatalf("partial update: %v", err)
	}
	if got.Version != "2.0" || got.Summary != summary {
		t.Fatalf("partial update clobbered fields: %+v", got)
	}

	// Unknown ID fails.
	if _, err := db.UpdateCapability(common.FormatHash(common.HashID("missing")), CapabilityPatch{}); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("missing id: want ErrNotFound, got %v", err)
	}

	// Invalid patch (mcp type without exactly one mcp resource) is rejected.
	badType := core.CapabilityMCP
	if _, err := db.UpdateCapability(id, CapabilityPatch{Type: &badType}); err == nil {
		t.Fatal("invalid type/resources combination must be rejected")
	}
}

func TestBuiltinCapabilitiesReadOnly(t *testing.T) {
	db := &DB{
		engine: newTestEngine(t),
		builtinCapabilities: []core.Capability{{
			Name: "内置手册", Type: core.CapabilitySkill, Status: core.CapabilityActive,
			Origin: core.CapabilityOriginBuiltin, IDHash: core.CapabilityID("内置手册"),
		}},
	}
	id := common.FormatHash(core.CapabilityID("内置手册"))
	if _, err := db.ActivateCapability(id); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("activate builtin: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.RecordCapabilityUsage(id, true); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("usage builtin: want ErrInvalidQuery, got %v", err)
	}
	if err := db.DeleteCapability(id); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("delete builtin: want ErrInvalidQuery, got %v", err)
	}
	if _, err := db.UpdateCapability(id, CapabilityPatch{}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("update builtin: want ErrInvalidQuery, got %v", err)
	}
}

func idsOfCapabilities(caps []core.Capability) []uint64 {
	out := make([]uint64, len(caps))
	for i, c := range caps {
		out[i] = c.IDHash
	}
	return out
}
