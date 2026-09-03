// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Offline interface tests: exercise the public API surface through
// api.OpenMulti against a mock OpenAI-compatible LLM server. No external
// services required; run with `go test ./test/...`.

package test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestInterfaceL5(t *testing.T) {
	db, _ := openTestDB(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "capability.json")
	content := `{"format":"memhop-capability/v3","name":"重构流程","version":"1","type":"mcp","summary":"重构代码","trigger":"用户要求重构","resources":[{"type":"mcp","name":"read_file"}]}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write capability file: %v", err)
	}

	cap, err := db.ImportCapability(path)
	if err != nil {
		t.Fatalf("ImportCapability: %v", err)
	}
	if cap == nil {
		t.Fatal("ImportCapability returned nil")
	}
	id := cap.IDHash
	got, err := db.GetCapability(id)
	if err != nil {
		t.Fatalf("GetCapability: %v", err)
	}
	if got.Name != "重构流程" || got.Type != core.CapabilityMCP {
		t.Fatalf("capability mismatch: %+v", got)
	}
	caps, err := db.ListCapabilities(internal.CapabilityListQuery{})
	if err != nil {
		t.Fatalf("ListCapabilities: %v", err)
	}
	// The response includes the read-only built-in toolbox; the imported
	// capability must be present and every other entry must be a built-in.
	found := false
	for _, c := range caps {
		if c.Name == "重构流程" {
			found = true
			continue
		}
		if c.Origin != core.CapabilityOriginBuiltin {
			t.Fatalf("unexpected non-builtin capability: %+v", c)
		}
	}
	if !found {
		t.Fatal("imported capability missing from list")
	}

	if err := db.DeleteCapability(id); err != nil {
		t.Fatalf("DeleteCapability: %v", err)
	}
	caps, err = db.ListCapabilities(internal.CapabilityListQuery{})
	if err != nil {
		t.Fatalf("ListCapabilities after delete: %v", err)
	}
	for _, c := range caps {
		if c.Origin != core.CapabilityOriginBuiltin {
			t.Fatalf("stored capability should be deleted: %+v", c)
		}
	}
}

func TestInterfaceL6(t *testing.T) {
	db, _ := openTestDB(t)
	session := "0000000000000001"
	ts := time.Now().UnixMilli()

	if err := db.AppendTrajectory(session, api.TrajectorySlot{
		EventType: "tool_call", Payload: `{"tool":"read_file","file":"a.go"}`, Timestamp: ts,
	}); err != nil {
		t.Fatalf("AppendTrajectory: %v", err)
	}
	if err := db.AppendTrajectory(session, api.TrajectorySlot{
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

	// Crystallize turns the trajectory into an L5 plugin via the mock LLM.
	res, err := db.Crystallize(context.Background(), session)
	if err != nil {
		t.Fatalf("Crystallize: %v", err)
	}
	if len(res.CreatedIDs) != 1 {
		t.Fatalf("want 1 created capability id: %+v", res)
	}
	// Built-ins are all active, so filtering by draft isolates the
	// crystallized capability.
	draft := core.CapabilityDraft
	caps, err := db.ListCapabilities(internal.CapabilityListQuery{Status: &draft})
	if err != nil {
		t.Fatalf("ListCapabilities after crystallize: %v", err)
	}
	if len(caps) != 1 || caps[0].Status != core.CapabilityDraft {
		t.Fatalf("want 1 draft capability after crystallize, got %d", len(caps))
	}
}
