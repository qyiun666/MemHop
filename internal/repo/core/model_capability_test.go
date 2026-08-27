// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"testing"
)

func TestCapabilityRoundtrip(t *testing.T) {
	cfg := `{"endpoint":"http://localhost:9000"}`
	c := Capability{
		IDHash: 123456789, Name: "Deploy Service", Version: "1",
		Type: CapabilityComposite, Summary: "deploy service", Trigger: "deploy", Status: CapabilityActive,
		SuccessRate: 0.92, TriggerCount: 5,
		LastTriggered: 1000000,
		Resources: []ResourceRef{
			{Type: CapabilitySkill, Name: "deploy-checklist", Desc: "pre-deploy checks"},
			{Type: CapabilityMCP, Name: "deploy-mcp", Ref: "localhost:9000", Config: &cfg},
			{Type: CapabilityMCP, Name: "run_test"},
		},
		Workflow: &Workflow{Steps: []WorkflowStep{
			{Ref: "deploy-checklist", Action: "run checks"},
			{Ref: "deploy-mcp", Action: "deploy"},
		}},
		CreatedAt: 900000, UpdatedAt: 950000,
	}
	var got Capability
	jsonRoundtrip(t, c, &got)
	if got.Status != CapabilityActive || got.SuccessRate != 0.92 {
		t.Fatalf("mismatch: %+v", got)
	}
	if len(got.Resources) != 3 || got.Resources[0].Type != CapabilitySkill ||
		got.Resources[1].Name != "deploy-mcp" || got.Resources[2].Name != "run_test" {
		t.Fatalf("resources mismatch: %+v", got.Resources)
	}
	if got.Workflow == nil || len(got.Workflow.Steps) != 2 || got.Workflow.Steps[1].Ref != "deploy-mcp" {
		t.Fatalf("workflow mismatch: %+v", got.Workflow)
	}
}

func TestCapabilityAllStatuses(t *testing.T) {
	statuses := []CapabilityStatus{CapabilityDraft, CapabilityActive, CapabilityDeprecated}
	for _, s := range statuses {
		c := Capability{IDHash: 1, Status: s}
		var got Capability
		jsonRoundtrip(t, c, &got)
		if got.Status != s {
			t.Fatalf("status mismatch: want %d got %d", s, got.Status)
		}
	}
}

func TestCapabilityResourcesOmitted(t *testing.T) {
	c := Capability{IDHash: 1, Resources: []ResourceRef{{Type: CapabilityMCP, Name: "s"}}}
	var got Capability
	jsonRoundtrip(t, c, &got)
	if len(got.Resources) != 1 || got.Workflow != nil {
		t.Fatalf("mismatch: %+v", got)
	}
}

func TestResourceRefConfigNil(t *testing.T) {
	p := ResourceRef{Type: CapabilityMCP, Name: "tool", Config: nil}
	var got ResourceRef
	jsonRoundtrip(t, p, &got)
	if got.Config != nil {
		t.Fatalf("expected nil config")
	}
}

func TestCapabilityPathRoundtrip(t *testing.T) {
	c := Capability{
		IDHash: 1, Name: "t", Trigger: "tr", Status: CapabilityActive,
		Resources: []ResourceRef{{Type: CapabilityMCP, Name: "tool", Ref: "session:abc"}},
	}
	var got Capability
	jsonRoundtrip(t, c, &got)
	if len(got.Resources) != 1 || got.Resources[0].Ref != "session:abc" {
		t.Fatalf("path mismatch: %+v", got)
	}
}
