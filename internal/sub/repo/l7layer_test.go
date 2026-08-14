// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

func TestAppendReadTrajectoryOrdered(t *testing.T) {
	engine := tempEngine(t)
	// Append out of order; Read must return by Seq ascending.
	if err := AppendTrajectory(engine, core.TrajectorySlot{SessionID: 7, Seq: 2, EventType: "tool_call", Payload: "b", Timestamp: 200}); err != nil {
		t.Fatalf("append seq2: %v", err)
	}
	if err := AppendTrajectory(engine, core.TrajectorySlot{SessionID: 7, Seq: 1, EventType: "turn_start", Payload: "a", Timestamp: 100}); err != nil {
		t.Fatalf("append seq1: %v", err)
	}
	// Another session must not leak in.
	if err := AppendTrajectory(engine, core.TrajectorySlot{SessionID: 8, Seq: 1, EventType: "turn_start", Payload: "other", Timestamp: 100}); err != nil {
		t.Fatalf("append other: %v", err)
	}
	events, err := ReadTrajectory(engine, 7)
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
		if err := AppendTrajectory(engine, core.TrajectorySlot{SessionID: 5, Seq: i, EventType: "turn_start", Timestamp: int64(i)}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := DeleteTrajectory(engine, 5); err != nil {
		t.Fatalf("delete: %v", err)
	}
	events, err := ReadTrajectory(engine, 5)
	if err != nil {
		t.Fatalf("read after delete: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("want empty trajectory, got %d events", len(events))
	}
	// Deleting a session without trajectory is a no-op.
	if err := DeleteTrajectory(engine, 999); err != nil {
		t.Fatalf("delete missing session: %v", err)
	}
}

func TestCreateOrUpdatePluginL5PersistsManifest(t *testing.T) {
	engine := tempEngine(t)
	path := "session:abc"
	cfg := `{"endpoint":"http://localhost:9000"}`
	manifest := core.PluginManifest{
		Skills: []core.PluginItem{{Name: "deploy-checklist"}},
		MCPs:   []core.PluginItem{{Name: "deploy-mcp", Config: &cfg}},
		Tools:  []core.PluginItem{{Name: "run_test"}},
	}
	pluginID, existed, err := CreateOrUpdatePluginL5(engine, "整理代码", "用户要求重构", "workflow", manifest, &path)
	if err != nil {
		t.Fatalf("create plugin: %v", err)
	}
	if existed {
		t.Fatal("fresh plugin should not exist yet")
	}
	plugin, err := GetPluginL5(engine, common.FormatHash(pluginID))
	if err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	if plugin.Path == nil || *plugin.Path != "session:abc" {
		t.Fatalf("path not persisted: %+v", plugin)
	}
	if plugin.PluginType != "workflow" || len(plugin.Manifest.Skills) != 1 ||
		len(plugin.Manifest.MCPs) != 1 || len(plugin.Manifest.Tools) != 1 {
		t.Fatalf("manifest mismatch: %+v", plugin)
	}
	if plugin.Manifest.MCPs[0].Config == nil || *plugin.Manifest.MCPs[0].Config != cfg {
		t.Fatalf("mcp config mismatch")
	}
}

func TestCreateOrUpdatePluginL5PreservesFields(t *testing.T) {
	engine := tempEngine(t)
	path1 := "session:a"
	id, existed, err := CreateOrUpdatePluginL5(engine, "t", "tr", "skill", core.PluginManifest{Skills: []core.PluginItem{{Name: "s1"}}}, &path1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if existed {
		t.Fatal("fresh plugin should not exist yet")
	}
	// Host accumulates runtime fields.
	plugin, err := GetPluginL5(engine, common.FormatHash(id))
	if err != nil {
		t.Fatalf("get plugin: %v", err)
	}
	plugin.Confidence = 0.8
	plugin.TriggerCount = 3
	if err := UpdatePluginL5(engine, common.FormatHash(id), plugin); err != nil {
		t.Fatalf("update plugin: %v", err)
	}
	// Re-create with a new path and manifest: runtime fields must survive,
	// Path/Manifest/PluginType refreshed.
	path2 := "session:b"
	id2, existed, err := CreateOrUpdatePluginL5(engine, "t", "tr", "workflow", core.PluginManifest{Tools: []core.PluginItem{{Name: "tool"}}}, &path2)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !existed || id2 != id {
		t.Fatalf("expected existing plugin id %d, got %d (existed=%v)", id, id2, existed)
	}
	got, err := GetPluginL5(engine, common.FormatHash(id2))
	if err != nil {
		t.Fatalf("get updated plugin: %v", err)
	}
	if got.Confidence != 0.8 || got.TriggerCount != 3 {
		t.Fatalf("runtime fields lost: %+v", got)
	}
	if got.PluginType != "workflow" || len(got.Manifest.Tools) != 1 {
		t.Fatalf("manifest not refreshed: %+v", got)
	}
	if got.Path == nil || *got.Path != "session:b" {
		t.Fatalf("path not refreshed: %+v", got)
	}
}

func TestDeletePluginL5(t *testing.T) {
	engine := tempEngine(t)
	id, _, err := CreateOrUpdatePluginL5(engine, "t", "tr", "skill", core.PluginManifest{}, nil)
	if err != nil {
		t.Fatalf("create plugin: %v", err)
	}
	if !DeletePluginL5(engine, common.FormatHash(id)) {
		t.Fatalf("delete plugin failed")
	}
	if plugins := core.CollectAllPlugins(engine); len(plugins) != 0 {
		t.Fatalf("want 0 plugins, got %+v", plugins)
	}
	// Deleting a missing record is idempotent (no error).
	if !DeletePluginL5(engine, common.FormatHash(id)) {
		t.Fatalf("second delete should stay idempotent")
	}
}
