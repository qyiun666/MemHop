// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package capability

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

func pkgDoc() CapabilityPackageDoc {
	return CapabilityPackageDoc{
		Format: FormatV4,
		Name:   "fs-tools",
		Capabilities: []CapabilityImport{
			{
				Name:      "read-file",
				Summary:   "read a file",
				Trigger:   "user asks to read",
				Resources: []ResourceRef{{Type: CapabilityMCP, Name: "fs.read"}},
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
	if cap.Name != "read-file" || cap.Version != "" || cap.Summary != "read a file" {
		t.Fatalf("definition mismatch: %+v", cap)
	}
	if len(cap.Resources) != 1 || cap.Resources[0].Type != CapabilityMCP {
		t.Fatalf("resources mismatch: %+v", cap.Resources)
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
	dup.Capabilities = append(dup.Capabilities, CapabilityImport{
		Name: "READ-FILE", Summary: "dup", Resources: []ResourceRef{{Name: "x"}},
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
		mutate  func(*CapabilityImport)
		wantErr bool
	}{
		{"mixed-kind entries are one card", func(i *CapabilityImport) {
			i.Resources = append(i.Resources, ResourceRef{Type: CapabilitySkill, Name: "b"})
		}, false},
		{"composite chain with tool keys", func(i *CapabilityImport) {
			cfg := `{"steps":[{"tool":"a"},{"tool":"b","args":{"x":1}}]}`
			i.Resources[0].Config = &cfg
		}, false},
		{"composite chain missing tool key", func(i *CapabilityImport) {
			cfg := `{"steps":[{"ref":"a"}]}`
			i.Resources[0].Config = &cfg
		}, true},
		{"malformed json config", func(i *CapabilityImport) {
			cfg := "{not json"
			i.Resources[0].Config = &cfg
		}, true},
		{"loose line config passes unchecked", func(i *CapabilityImport) {
			cfg := "bash: ls -la"
			i.Resources[0].Config = &cfg
		}, false},
		{"no resources", func(i *CapabilityImport) { i.Resources = nil }, true},
		{"missing name", func(i *CapabilityImport) { i.Name = "  " }, true},
		{"missing trigger and summary", func(i *CapabilityImport) {
			i.Trigger, i.Summary = "", ""
		}, true},
		{"unknown resource type", func(i *CapabilityImport) {
			i.Resources[0].Type = "mcp "
		}, true},
		{"missing resource type", func(i *CapabilityImport) {
			i.Resources[0].Type = ""
		}, true},
		{"invalid json schema input", func(i *CapabilityImport) {
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

// TestPromptCardRendersCallContract pins the prompt rendering: the call
// contract per resource (including the steps line) is in, and every
// record-layer leftover (stored id / package stamp / usage statistics) is out.
func TestPromptCardRendersCallContract(t *testing.T) {
	cfg := `{"steps":[{"tool":"a"},{"tool":"b"}]}`
	card := CapabilityImport{
		Name: "deploy", Version: "2", Summary: "ship it", Trigger: "on release",
		Resources: []ResourceRef{
			{Type: CapabilitySkill, Name: "s", Desc: "run it"},
			{Type: CapabilityComposite, Name: "chain", Config: &cfg},
		},
	}
	out := card.PromptCard()
	for _, want := range []string{
		"[capability: deploy]", "version: 2", "summary: ship it", "trigger: on release",
		"resource: skill s", "  use: run it", "resource: composite chain", "  steps: a -> b",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("PromptCard missing %q:\n%s", want, out)
		}
	}
	for _, banned := range []string{"id:", "package:", "usage:"} {
		if strings.Contains(out, banned) {
			t.Fatalf("PromptCard must not render %q (record-layer leftover):\n%s", banned, out)
		}
	}
}
