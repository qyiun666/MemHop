// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests: exercise the public API surface through
// api.OpenMulti against a mock OpenAI-compatible LLM server. No external
// services required; run with `go test ./test/...`.

package test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/api"
)

func TestInterfaceL6(t *testing.T) {
	db, _ := openTestDB(t)
	sceneID := openSession(t, db)
	// The trajectory key is a turn's topic id — minted by Search, settled by
	// Update, and never typed by hand.
	session := openTurn(t, db, sceneID)
	if _, err := db.Update(turn(sceneID, session, "读一下 a.go 并改掉拼写", "已读取 a.go 并改掉拼写")); err != nil {
		t.Fatalf("Update: %v", err)
	}
	ts := time.Now().UnixMilli()

	if err := db.AppendTrajectory(session, "", api.TrajectorySlot{
		EventType: "tool_call", Payload: `{"tool":"read_file","file":"a.go"}`, Timestamp: ts,
	}); err != nil {
		t.Fatalf("AppendTrajectory: %v", err)
	}
	// The key has to be a turn the library actually opened, or the rest of this
	// test would only prove that a made-up id round-trips.
	if surface, err := db.Search(api.SearchQuery{SceneID: sceneID}); err != nil ||
		!slices.ContainsFunc(surface.Topics, func(topic api.TopicSlot) bool { return topic.ID == session }) {
		t.Fatalf("key %s is not a topic of scene %s: %+v err %v", session, sceneID, surface.Topics, err)
	}
	if err := db.AppendTrajectory(session, "", api.TrajectorySlot{
		EventType: "tool_result", Payload: "file content", Timestamp: ts + 500,
	}); err != nil {
		t.Fatalf("AppendTrajectory #2: %v", err)
	}
	events, err := db.ReadTrajectory(session)
	if err != nil {
		t.Fatalf("ReadTrajectory: %v", err)
	}
	if len(events) != 2 || events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("want 2 events with seq 1,2: %+v", events)
	}

	// Crystallize returns candidates against a host-supplied catalog; the
	// engine stores nothing, so the candidates are all there is. The mock
	// speaks the prompt's contract: reuse/merge name an existing card,
	// create carries a full v4 card.
	existing := []api.CapabilityImport{{
		Name: "已有能力", Summary: "已有", Trigger: "已有触发",
		Resources: []api.ResourceRef{{Type: api.CapabilityMCP, Name: "old_tool"}},
	}}
	out, err := db.Crystallize(context.Background(), session, existing)
	if err != nil {
		t.Fatalf("Crystallize: %v", err)
	}
	if len(out.Capabilities) != 3 {
		t.Fatalf("want 3 candidates (reuse/merge/create): %+v", out)
	}
	byAction := map[string]api.CrystallizeCapability{}
	for _, c := range out.Capabilities {
		byAction[c.Action] = c
	}
	if reuse := byAction["reuse"]; reuse.ReuseID != "已有能力" {
		t.Fatalf("reuse candidate must name the existing card: %+v", reuse)
	}
	if merge := byAction["merge"]; merge.ReuseID != "已有能力" || merge.Capability.Name != "已有能力" {
		t.Fatalf("merge candidate mismatch: %+v", merge)
	}
	if create := byAction["create"]; create.Capability.Name != "重构流程" ||
		len(create.Capability.Resources) != 1 || create.Capability.Resources[0].Name != "read_file" {
		t.Fatalf("create candidate mismatch: %+v", create)
	}
}
