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
	if err := core.WriteCapability(engine, core.SharedPoolAgentID, c.IDHash, c); err != nil {
		t.Fatalf("write capability: %v", err)
	}
}

func TestListCapabilities(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	c1 := core.Capability{IDHash: common.HashID("c1"), Name: "修复编译错误", Status: core.CapabilityActive, UpdatedAt: 3000}
	c2 := core.Capability{IDHash: common.HashID("c2"), Name: "代码审查流程", Status: core.CapabilityDraft, UpdatedAt: 1000}
	c3 := core.Capability{IDHash: common.HashID("c3"), Name: "发布版本", Status: core.CapabilityActive, Package: "release", UpdatedAt: 2000}
	writeCapability(t, engine, &c1)
	writeCapability(t, engine, &c2)
	writeCapability(t, engine, &c3)

	out, err := db.ListCapabilities(core.DefaultAgentID, CapabilityListQuery{})
	if err != nil {
		t.Fatalf("ListCapabilities: %v", err)
	}
	if len(out) != 3 || out[0].IDHash != c1.IDHash || out[1].IDHash != c3.IDHash || out[2].IDHash != c2.IDHash {
		t.Fatalf("all: want [c1 c3 c2], got %v", idsOfCapabilities(out))
	}

	active := core.CapabilityActive
	out, err = db.ListCapabilities(core.DefaultAgentID, CapabilityListQuery{Status: &active})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("status active: want 2, got %d", len(out))
	}

	pkg := "release"
	out, err = db.ListCapabilities(core.DefaultAgentID, CapabilityListQuery{Package: &pkg})
	if err != nil {
		t.Fatalf("package: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != c3.IDHash {
		t.Fatalf("package release: want [c3], got %v", idsOfCapabilities(out))
	}

	out, err = db.ListCapabilities(core.DefaultAgentID, CapabilityListQuery{Keyword: "编译"})
	if err != nil {
		t.Fatalf("keyword: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != c1.IDHash {
		t.Fatalf("keyword: want [c1], got %v", idsOfCapabilities(out))
	}
}

func TestListCapabilitiesEmpty(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	out, err := db.ListCapabilities(core.DefaultAgentID, CapabilityListQuery{})
	if err != nil {
		t.Fatalf("ListCapabilities: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("want 0 capabilities, got %d", len(out))
	}
}

// The L5 pool is file-wide: a card imported by one agent is listed by every
// other agent, and deleting the importing agent leaves the pool intact.
func TestCapabilityPoolSharedAcrossAgents(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	a, err := db.CreateAgent("猫A")
	if err != nil {
		t.Fatalf("create agent A: %v", err)
	}
	b, err := db.CreateAgent("猫B")
	if err != nil {
		t.Fatalf("create agent B: %v", err)
	}
	path := writeTempCapability(t, t.TempDir(), "cap.json", testCapabilityJSON)
	if _, err := db.ImportCapability(a, path); err != nil {
		t.Fatalf("import by agent A: %v", err)
	}
	out, err := db.ListCapabilities(b, CapabilityListQuery{})
	if err != nil {
		t.Fatalf("list by agent B: %v", err)
	}
	if len(out) != 1 || out[0].Name != "测试工具" {
		t.Fatalf("agent B must see agent A's card, got %v", idsOfCapabilities(out))
	}
	if err := db.DeleteAgent(a); err != nil {
		t.Fatalf("delete agent A: %v", err)
	}
	out, err = db.ListCapabilities(b, CapabilityListQuery{})
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(out) != 1 || out[0].Name != "测试工具" {
		t.Fatalf("DeleteAgent must not touch the shared pool, got %v", idsOfCapabilities(out))
	}
}

func TestUpdateCapabilityActivates(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	cap := &core.Capability{Name: "待激活", Status: core.CapabilityDraft, IDHash: core.CapabilityID("待激活"), Summary: "s",
		Resources: []core.ResourceRef{{Type: core.CapabilitySkill, Name: "r", Desc: "call it"}}}
	writeCapability(t, db.engine, cap)
	id := common.FormatHash(cap.IDHash)

	active := core.CapabilityActive
	got, err := db.UpdateCapability(core.DefaultAgentID, id, CapabilityPatch{Status: &active})
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
	if _, err := db.UpdateCapability(core.DefaultAgentID, id, CapabilityPatch{Status: &active}); err != nil {
		t.Fatalf("re-activate: %v", err)
	}
	// Unknown IDs surface ErrNotFound instead of inventing a record.
	if _, err := db.UpdateCapability(core.DefaultAgentID, common.FormatHash(common.HashID("missing")), CapabilityPatch{Status: &active}); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("missing id: want ErrNotFound, got %v", err)
	}
}

func TestRecordCapabilityUsage(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	cap := &core.Capability{Name: "用量能力", Status: core.CapabilityActive, IDHash: core.CapabilityID("用量能力")}
	writeCapability(t, db.engine, cap)
	id := common.FormatHash(cap.IDHash)

	got, err := db.RecordCapabilityUsage(core.DefaultAgentID, id, true)
	if err != nil {
		t.Fatalf("first usage: %v", err)
	}
	if got.TriggerCount != 1 || got.SuccessRate != 1.0 {
		t.Fatalf("first success: %+v", got)
	}

	got, err = db.RecordCapabilityUsage(core.DefaultAgentID, id, false)
	if err != nil {
		t.Fatalf("second usage: %v", err)
	}
	if got.TriggerCount != 2 || got.SuccessRate != 0.5 {
		t.Fatalf("second failure: %+v", got)
	}

	if _, err := db.RecordCapabilityUsage(core.DefaultAgentID, common.FormatHash(common.HashID("missing")), true); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("missing id: want ErrNotFound, got %v", err)
	}
}

func TestUpdateCapability(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	cap := &core.Capability{Name: "可更新", Status: core.CapabilityActive, IDHash: core.CapabilityID("可更新"), FileHash: "pkg-hash"}
	writeCapability(t, db.engine, cap)
	id := common.FormatHash(cap.IDHash)

	summary := "新的摘要"
	trigger := "新触发词"
	resources := []core.ResourceRef{
		{Type: core.CapabilityMCP, Name: "m1"},
		{Type: core.CapabilitySkill, Name: "s1"},
	}

	got, err := db.UpdateCapability(core.DefaultAgentID, id, CapabilityPatch{
		Summary: &summary, Trigger: &trigger, Resources: &resources,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Summary != summary || got.Trigger != trigger {
		t.Fatalf("fields not applied: %+v", got)
	}
	if len(got.Resources) != 2 {
		t.Fatalf("resources not applied: %+v", got)
	}
	// FileHash is the package watermark, not a content fingerprint: an
	// update keeps it, so re-importing the same package bytes stays a no-op
	// and the host's edit is not silently reverted at restart.
	if got.FileHash != "pkg-hash" {
		t.Fatalf("FileHash must survive an update, got %q", got.FileHash)
	}

	// Partial update leaves untouched fields unchanged.
	version := "2.0"
	got, err = db.UpdateCapability(core.DefaultAgentID, id, CapabilityPatch{Version: &version})
	if err != nil {
		t.Fatalf("partial update: %v", err)
	}
	if got.Version != "2.0" || got.Summary != summary {
		t.Fatalf("partial update clobbered fields: %+v", got)
	}

	// Unknown ID fails.
	if _, err := db.UpdateCapability(core.DefaultAgentID, common.FormatHash(common.HashID("missing")), CapabilityPatch{}); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("missing id: want ErrNotFound, got %v", err)
	}

	// A patch that would strip every function entry is rejected.
	empty := []core.ResourceRef{}
	if _, err := db.UpdateCapability(core.DefaultAgentID, id, CapabilityPatch{Resources: &empty}); err == nil {
		t.Fatal("resources-less capability must be rejected")
	}
}

// The built-in manuals are not stored records: they show up in listings, but
// every write path reports ErrNotFound for their ids, exactly like any record
// that is not there.
func TestBuiltinCardsNotStored(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	db.builtinCapabilities = BuiltinCards()
	id := common.FormatHash(core.CapabilityID("memhop-guide"))

	if _, err := db.ListCapabilities(core.DefaultAgentID, CapabilityListQuery{}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if _, err := db.RecordCapabilityUsage(core.DefaultAgentID, id, true); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("usage builtin: want ErrNotFound, got %v", err)
	}
	if err := db.DeleteCapability(core.DefaultAgentID, id); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("delete builtin: want ErrNotFound, got %v", err)
	}
	if _, err := db.UpdateCapability(core.DefaultAgentID, id, CapabilityPatch{}); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("update builtin: want ErrNotFound, got %v", err)
	}
}

func idsOfCapabilities(caps []core.Capability) []uint64 {
	out := make([]uint64, len(caps))
	for i, c := range caps {
		out[i] = c.IDHash
	}
	return out
}
