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

func pkgDoc() core.CapabilityPackageDoc {
	return core.CapabilityPackageDoc{
		Format: FormatV4,
		Name:   "fs-tools",
		Capabilities: []core.CapabilityImport{
			{
				Name:      "read-file",
				Summary:   "read a file",
				Trigger:   "user asks to read",
				Resources: []core.ResourceRef{{Type: core.CapabilityMCP, Name: "fs.read"}},
			},
		},
	}
}

func TestBuildPackageParsesValidDocument(t *testing.T) {
	data, err := json.Marshal(pkgDoc())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	caps, err := BuildPackage(data, "test")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(caps) != 1 {
		t.Fatalf("want 1 card, got %d", len(caps))
	}
	cap := caps[0]
	if cap.Name != "read-file" || cap.Version != "1" || cap.Package != "fs-tools" {
		t.Fatalf("definition mismatch: %+v", cap)
	}
	if len(cap.FileHash) != 64 {
		t.Fatalf("file hash must be sha256 hex, got %q", cap.FileHash)
	}
}

func TestBuildPackageRejectsBadFormatAndDuplicates(t *testing.T) {
	bad := pkgDoc()
	bad.Format = "memhop-capability/v3"
	data, _ := json.Marshal(bad)
	if _, err := BuildPackage(data, "t"); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("v3 document must be rejected explicitly, got %v", err)
	}
	dup := pkgDoc()
	dup.Capabilities = append(dup.Capabilities, core.CapabilityImport{
		Name: "READ-FILE", Summary: "dup", Resources: []core.ResourceRef{{Name: "x"}},
	})
	data, _ = json.Marshal(dup)
	if _, err := BuildPackage(data, "t"); err == nil {
		t.Fatal("duplicate capability name (case-insensitive) must be rejected")
	}
	empty := pkgDoc()
	empty.Capabilities = nil
	data, _ = json.Marshal(empty)
	if _, err := BuildPackage(data, "t"); err == nil {
		t.Fatal("package without cards must be rejected")
	}
	unnamed := pkgDoc()
	unnamed.Name = " "
	data, _ = json.Marshal(unnamed)
	if _, err := BuildPackage(data, "t"); err == nil {
		t.Fatal("package without a name must be rejected")
	}
}

func TestValidateCardMatrix(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*core.CapabilityImport)
		wantErr bool
	}{
		{"mixed-kind entries are one card", func(i *core.CapabilityImport) {
			i.Resources = append(i.Resources, core.ResourceRef{Type: core.CapabilitySkill, Name: "b"})
		}, false},
		{"composite chain with tool keys", func(i *core.CapabilityImport) {
			cfg := `{"steps":[{"tool":"a"},{"tool":"b","args":{"x":1}}]}`
			i.Resources[0].Config = &cfg
		}, false},
		{"composite chain missing tool key", func(i *core.CapabilityImport) {
			cfg := `{"steps":[{"ref":"a"}]}`
			i.Resources[0].Config = &cfg
		}, true},
		{"malformed json config", func(i *core.CapabilityImport) {
			cfg := "{not json"
			i.Resources[0].Config = &cfg
		}, true},
		{"loose line config passes unchecked", func(i *core.CapabilityImport) {
			cfg := "bash: ls -la"
			i.Resources[0].Config = &cfg
		}, false},
		{"no resources", func(i *core.CapabilityImport) { i.Resources = nil }, true},
		{"missing name", func(i *core.CapabilityImport) { i.Name = "  " }, true},
		{"missing trigger and summary", func(i *core.CapabilityImport) {
			i.Trigger, i.Summary = "", ""
		}, true},
		{"unknown resource type", func(i *core.CapabilityImport) {
			i.Resources[0].Type = "mcp "
		}, true},
		{"missing resource type", func(i *core.CapabilityImport) {
			i.Resources[0].Type = ""
		}, true},
		{"invalid json schema input", func(i *core.CapabilityImport) {
			i.Resources[0].Input = "{not json"
		}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := pkgDoc()
			tc.mutate(&doc.Capabilities[0])
			err := ValidateCard(&doc.Capabilities[0])
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateCard() err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestMergeDefinitionKeepsIdentityAndUsage(t *testing.T) {
	existing := &core.Capability{
		Name: "keep", IDHash: 7, Status: core.CapabilityActive, Origin: core.CapabilityOriginImported,
		TriggerCount: 12, SuccessRate: 0.9, Summary: "old", Version: "1", Package: "fs-tools",
	}
	incoming := BuildCrystallized(&core.CapabilityImport{
		Name: "incoming", Summary: "new", Trigger: "trig",
		Resources: []core.ResourceRef{{Type: core.CapabilitySkill, Name: "s"}},
	}, 1000)
	MergeDefinition(existing, incoming, 2000)
	if existing.Summary != "new" || existing.Version != "1" || existing.Trigger != "trig" {
		t.Fatalf("definition not merged: %+v", existing)
	}
	if existing.IDHash != 7 || existing.Name != "keep" || existing.TriggerCount != 12 || existing.SuccessRate != 0.9 {
		t.Fatalf("identity/usage must survive merge: %+v", existing)
	}
	if existing.Package != "fs-tools" {
		t.Fatalf("package stamp must survive merge: %+v", existing)
	}
	if existing.UpdatedAt != 2000 {
		t.Fatalf("UpdatedAt = %d; want 2000", existing.UpdatedAt)
	}
}

func TestMatchesAndActiveOnly(t *testing.T) {
	active := core.Capability{Name: "deploy-runbook", Summary: "ship it", Status: core.CapabilityActive, Package: "fs-tools"}
	draft := core.Capability{Name: "old-card", Summary: "retired", Status: core.CapabilityDraft}
	caps := []core.Capability{active, draft}
	if got := ActiveOnly(append([]core.Capability(nil), caps...)); len(got) != 1 || got[0].Name != "deploy-runbook" {
		t.Fatalf("ActiveOnly = %+v", got)
	}
	other := "other"
	checks := []struct {
		q    core.CapabilityListQuery
		want bool
	}{
		{core.CapabilityListQuery{}, true},
		{core.CapabilityListQuery{Keyword: "SHIP"}, true}, // case-insensitive over name+summary+trigger
		{core.CapabilityListQuery{Keyword: "cooking"}, false},
		{core.CapabilityListQuery{Status: &draft.Status}, false},
		{core.CapabilityListQuery{Package: &active.Package}, true},
		{core.CapabilityListQuery{Package: &other}, false},
	}
	for _, tc := range checks {
		if got := Matches(&active, &tc.q, strings.ToLower(tc.q.Keyword)); got != tc.want {
			t.Fatalf("Matches(%+v) = %v; want %v", tc.q, got, tc.want)
		}
	}
}
