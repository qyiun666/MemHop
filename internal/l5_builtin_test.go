// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func testBuiltinCapabilities() []core.Capability {
	return []core.Capability{
		{
			IDHash: core.CapabilityID("内置手册"), Name: "内置手册",
			Type: core.CapabilitySkill, Status: core.CapabilityActive,
			Origin: core.CapabilityOriginBuiltin, Trigger: "检索 记忆",
			Resources: []core.ResourceRef{{Type: core.CapabilitySkill, Name: "内置手册"}},
		},
		{
			IDHash: core.CapabilityID("内置工具"), Name: "内置工具",
			Type: core.CapabilityMCP, Status: core.CapabilityActive,
			Origin: core.CapabilityOriginBuiltin, Trigger: "工具",
			Resources: []core.ResourceRef{{Type: core.CapabilityMCP, Name: "内置工具"}},
		},
	}
}

// The L5 read APIs serve the built-in toolbox alongside stored records:
// built-ins pass the same list filters and are never written to the engine.
func TestListCapabilitiesWithBuiltins(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	db.SetBuiltinCapabilities(testBuiltinCapabilities())
	stored := core.Capability{IDHash: common.HashID("stored"), Name: "库存能力", Type: core.CapabilityMCP, Status: core.CapabilityActive, UpdatedAt: 1000}
	writeCapability(t, engine, &stored)

	out, err := db.ListCapabilities(CapabilityListQuery{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("want stored + 2 builtins, got %d", len(out))
	}

	// Filters apply to built-ins too (they are active mcp/skill cards).
	draft := core.CapabilityDraft
	out, err = db.ListCapabilities(CapabilityListQuery{Status: &draft})
	if err != nil {
		t.Fatalf("list draft: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("builtins must not match draft filter, got %d", len(out))
	}

	skill := core.CapabilitySkill
	out, err = db.ListCapabilities(CapabilityListQuery{Type: &skill})
	if err != nil {
		t.Fatalf("list skill: %v", err)
	}
	if len(out) != 1 || out[0].Name != "内置手册" {
		t.Fatalf("type filter mismatch: %v", idsOfCapabilities(out))
	}

	// Listing never persists built-ins.
	if got := len(core.CollectAllCapabilities(engine)); got != 1 {
		t.Fatalf("builtins must not be stored, want 1 stored record, got %d", got)
	}
}

// A stored record with the same name/ID wins over its built-in twin.
func TestListCapabilitiesBuiltinDedup(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	db.SetBuiltinCapabilities(testBuiltinCapabilities())
	stored := core.Capability{
		IDHash: core.CapabilityID("内置手册"), Name: "内置手册",
		Type: core.CapabilitySkill, Status: core.CapabilityActive,
		Origin: core.CapabilityOriginImported, TriggerCount: 7, UpdatedAt: 1000,
	}
	writeCapability(t, engine, &stored)

	out, err := db.ListCapabilities(CapabilityListQuery{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	count := 0
	for _, c := range out {
		if c.IDHash == stored.IDHash {
			count++
			if c.Origin != core.CapabilityOriginImported || c.TriggerCount != 7 {
				t.Fatalf("stored record must win over builtin: origin=%v count=%d", c.Origin, c.TriggerCount)
			}
		}
	}
	if count != 1 {
		t.Fatalf("want exactly one copy, got %d", count)
	}
}

// GetCapability falls back to the built-in toolbox for IDs not stored in
// the file, so every listed capability stays retrievable.
func TestGetCapabilityBuiltinFallback(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	db.SetBuiltinCapabilities(testBuiltinCapabilities())

	id := common.FormatHash(core.CapabilityID("内置手册"))
	cap, err := db.GetCapability(id)
	if err != nil {
		t.Fatalf("get builtin: %v", err)
	}
	if cap.Name != "内置手册" || cap.Origin != core.CapabilityOriginBuiltin {
		t.Fatalf("builtin fallback mismatch: %+v", cap)
	}
	if _, err := db.GetCapability(common.FormatHash(common.HashID("不存在"))); err == nil {
		t.Fatal("unknown id must still fail")
	}
}

// Search capability matching stays pure retrieval over stored records:
// the built-in toolbox is NOT attached to Search responses.
func TestMatchCapabilitiesExcludesBuiltins(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	db.SetBuiltinCapabilities(testBuiltinCapabilities())

	out := db.matchCapabilities("内置手册 检索")
	if len(out) != 0 {
		t.Fatalf("search must not carry builtins, got %d", len(out))
	}
}
