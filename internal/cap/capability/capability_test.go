// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package capability

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func mcpDoc() core.CapabilityImport {
	return core.CapabilityImport{
		Format:    FormatV3,
		Name:      "read-file",
		Type:      core.CapabilityMCP,
		Summary:   "read a file",
		Trigger:   "user asks to read",
		Resources: []core.ResourceRef{{Type: core.CapabilityMCP, Name: "fs.read"}},
	}
}

func TestBuildParsesValidDocument(t *testing.T) {
	data, err := json.Marshal(mcpDoc())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	cap, err := Build(data, "test")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if cap.Name != "read-file" || cap.Version != "1" || cap.Type != core.CapabilityMCP {
		t.Fatalf("definition mismatch: %+v", cap)
	}
	if len(cap.FileHash) != 64 {
		t.Fatalf("file hash must be sha256 hex, got %q", cap.FileHash)
	}
}

func TestBuildRejectsBadFormatAndUnknownType(t *testing.T) {
	bad := mcpDoc()
	bad.Format = "memhop-capability/v2"
	data, _ := json.Marshal(bad)
	if _, err := Build(data, "t"); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("wrong format must be rejected, got %v", err)
	}
	unknown := mcpDoc()
	unknown.Type = core.CapabilityType("magic")
	data, _ = json.Marshal(unknown)
	if _, err := Build(data, "t"); err == nil {
		t.Fatal("unknown capability type must be rejected")
	}
}

func TestValidateTypeResourceMatrix(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*core.CapabilityImport)
		wantErr bool
	}{
		{"mcp single same-type resource", func(i *core.CapabilityImport) {}, false},
		{"mcp two resources", func(i *core.CapabilityImport) {
			i.Resources = append(i.Resources, core.ResourceRef{Type: core.CapabilityMCP, Name: "b"})
		}, true},
		{"mcp foreign resource type", func(i *core.CapabilityImport) {
			i.Resources = []core.ResourceRef{{Type: core.CapabilitySkill, Name: "b"}}
		}, true},
		{"composite needs resources", func(i *core.CapabilityImport) {
			i.Type = core.CapabilityComposite
			i.Resources = nil
		}, true},
		{"composite workflow step ref", func(i *core.CapabilityImport) {
			i.Type = core.CapabilityComposite
			i.Workflow = &core.Workflow{Steps: []core.WorkflowStep{{Ref: " "}}}
		}, true},
		{"missing name", func(i *core.CapabilityImport) { i.Name = "  " }, true},
		{"missing trigger and summary", func(i *core.CapabilityImport) {
			i.Trigger, i.Summary = "", ""
		}, true},
		{"invalid json schema input", func(i *core.CapabilityImport) {
			i.Resources[0].Input = "{not json"
		}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := mcpDoc()
			tc.mutate(&in)
			err := Validate(&in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestMergeDefinitionKeepsIdentityAndUsage(t *testing.T) {
	existing := &core.Capability{
		Name: "keep", IDHash: 7, Status: core.CapabilityActive, Origin: core.CapabilityOriginImported,
		TriggerCount: 12, SuccessRate: 0.9, Summary: "old", Version: "1",
	}
	incoming := BuildCrystallized(&core.CapabilityImport{
		Name: "incoming", Type: core.CapabilitySkill, Summary: "new", Trigger: "trig",
		Resources: []core.ResourceRef{{Type: core.CapabilitySkill, Name: "s"}},
	}, 1000)
	MergeDefinition(existing, incoming, 2000)
	if existing.Summary != "new" || existing.Version != "1" || existing.Trigger != "trig" {
		t.Fatalf("definition not merged: %+v", existing)
	}
	if existing.IDHash != 7 || existing.Name != "keep" || existing.TriggerCount != 12 || existing.SuccessRate != 0.9 {
		t.Fatalf("identity/usage must survive merge: %+v", existing)
	}
	if existing.UpdatedAt != 2000 {
		t.Fatalf("UpdatedAt = %d; want 2000", existing.UpdatedAt)
	}
}

func TestMatchesAndActiveOnly(t *testing.T) {
	active := core.Capability{Name: "deploy-runbook", Summary: "ship it", Status: core.CapabilityActive}
	draft := core.Capability{Name: "old-card", Summary: "retired", Status: core.CapabilityDraft}
	caps := []core.Capability{active, draft}
	if got := ActiveOnly(append([]core.Capability(nil), caps...)); len(got) != 1 || got[0].Name != "deploy-runbook" {
		t.Fatalf("ActiveOnly = %+v", got)
	}
	typeChecks := []struct {
		q    core.CapabilityListQuery
		want bool
	}{
		{core.CapabilityListQuery{}, true},
		{core.CapabilityListQuery{Keyword: "SHIP"}, true}, // case-insensitive over name+summary+trigger
		{core.CapabilityListQuery{Keyword: "cooking"}, false},
		{core.CapabilityListQuery{Status: &draft.Status}, false},
	}
	for _, tc := range typeChecks {
		if got := Matches(&active, &tc.q, strings.ToLower(tc.q.Keyword)); got != tc.want {
			t.Fatalf("Matches(%+v) = %v; want %v", tc.q, got, tc.want)
		}
	}
}
