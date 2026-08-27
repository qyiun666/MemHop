// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package llmops

import (
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestParseCrystallizeResponse(t *testing.T) {
	resp := `{
  "capabilities": [
    {"action": "create", "capability": {"name": "重构流程", "type": "composite", "summary": "重构", "trigger": "需要重构时", "resources": [
      {"type": "mcp", "name": "read_file", "config": "{\"file\":\"a.go\"}"}, {"type": "mcp", "name": "write_file"},
      {"type": "skill", "name": "s1", "desc": "d"}
    ]}},
    {"action": "reuse", "reuse_id": "a1b2c3d4e5f67890", "capability": {"name": "已有能力"}},
    {"action": "create", "capability": {"name": ""}}
  ]
}`
	out, err := parseCrystallizeResponse(resp)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out.Capabilities) != 2 {
		t.Fatalf("want 2 valid capabilities, got %d", len(out.Capabilities))
	}
	c := out.Capabilities[0]
	if c.Action != "create" || c.Capability.Name != "重构流程" || c.Capability.Type != core.CapabilityComposite {
		t.Fatalf("capability mismatch: %+v", c)
	}
	if len(c.Capability.Resources) != 3 || c.Capability.Resources[0].Name != "read_file" {
		t.Fatalf("resources mismatch: %+v", c.Capability.Resources)
	}
}

func TestBuildCrystallizePrompt(t *testing.T) {
	events := []core.TrajectorySlot{
		{Seq: 1, EventType: "tool_call", Payload: "read file"},
		{Seq: 2, EventType: "tool_result", Payload: "ok"},
	}
	existing := []core.Capability{{Name: "deploy-runbook", Type: core.CapabilitySkill, Summary: "部署", Trigger: "部署"}}
	prompt := buildCrystallizePrompt(events, existing)
	if strings.Contains(prompt, "read file") == false || strings.Contains(prompt, "deploy-runbook") == false {
		t.Fatalf("prompt missing inputs: %s", prompt)
	}
}
