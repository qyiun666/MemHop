// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package sub

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// writePlugin writes an L5 plugin record directly.
func writePlugin(t *testing.T, engine *core.StorageEngine, p *core.PluginSlot) {
	t.Helper()
	if err := core.WritePluginSlot(engine, p.IDHash, p); err != nil {
		t.Fatalf("write plugin: %v", err)
	}
}

// TestListPlugins covers all, filters (status/type/keyword) and ordering.
func TestListPlugins(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	p1 := core.PluginSlot{IDHash: common.HashID("p1"), Name: "修复编译错误", PluginType: "skill", Status: core.PluginActive, TriggerCount: 5, UpdatedAt: 3000}
	p2 := core.PluginSlot{IDHash: common.HashID("p2"), Name: "代码审查流程", PluginType: "workflow", Status: core.PluginDraft, TriggerCount: 1, UpdatedAt: 1000}
	p3 := core.PluginSlot{IDHash: common.HashID("p3"), Name: "发布版本", PluginType: "workflow", Status: core.PluginActive, TriggerCount: 2, UpdatedAt: 2000}
	writePlugin(t, engine, &p1)
	writePlugin(t, engine, &p2)
	writePlugin(t, engine, &p3)

	// All: sorted by UpdatedAt desc -> p1, p3, p2.
	out, err := db.ListPlugins(PluginListQuery{})
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if len(out) != 3 || out[0].IDHash != p1.IDHash || out[1].IDHash != p3.IDHash || out[2].IDHash != p2.IDHash {
		t.Fatalf("all: want [p1 p3 p2], got %v", idsOfPlugins(out))
	}

	// Status filter.
	out, err = db.ListPlugins(PluginListQuery{Status: strPtr("active")})
	if err != nil {
		t.Fatalf("ListPlugins status: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("status active: want 2 plugins, got %d", len(out))
	}

	// PluginType filter.
	tp := "workflow"
	out, err = db.ListPlugins(PluginListQuery{PluginType: &tp})
	if err != nil {
		t.Fatalf("ListPlugins type: %v", err)
	}
	if len(out) != 2 || out[0].IDHash != p3.IDHash {
		t.Fatalf("type workflow: want [p3 p2], got %v", idsOfPlugins(out))
	}

	// Keyword case-insensitive substring match on name.
	out, err = db.ListPlugins(PluginListQuery{Keyword: "编译"})
	if err != nil {
		t.Fatalf("ListPlugins keyword: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != p1.IDHash {
		t.Fatalf("keyword: want [p1], got %v", idsOfPlugins(out))
	}

	// Combined filters.
	out, err = db.ListPlugins(PluginListQuery{Status: strPtr("active"), Keyword: "发布"})
	if err != nil {
		t.Fatalf("ListPlugins combo: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != p3.IDHash {
		t.Fatalf("combo: want [p3], got %v", idsOfPlugins(out))
	}
}

// TestListPluginsEmpty empty db returns an empty slice.
func TestListPluginsEmpty(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	out, err := db.ListPlugins(PluginListQuery{})
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("want 0 plugins, got %d", len(out))
	}
}

func idsOfPlugins(plugins []core.PluginSlot) []uint64 {
	out := make([]uint64, len(plugins))
	for i, p := range plugins {
		out[i] = p.IDHash
	}
	return out
}
