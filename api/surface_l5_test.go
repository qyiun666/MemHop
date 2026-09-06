// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 capability document surface tests: the engine stores no capability
// records, so the facade's L5 face is the v4 parse/validate pair a host uses
// on its own capability directory.

package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// capabilityDoc returns a valid v4 single-card package document.
func capabilityDoc(name string) CapabilityPackageDoc {
	return CapabilityPackageDoc{
		Format: CapabilityFormatV4, Name: name,
		Capabilities: []CapabilityImport{{
			Name: name, Version: "1", Summary: "summarizes", Trigger: "when asked",
			Resources: []ResourceRef{{Type: CapabilitySkill, Name: name, Desc: "call it"}},
		}},
	}
}

func TestParseCapabilityPackage(t *testing.T) {
	data, err := json.Marshal(capabilityDoc("surface-cap"))
	if err != nil {
		t.Fatal(err)
	}
	caps, err := ParseCapabilityPackage(data, "test")
	if err != nil || len(caps) != 1 {
		t.Fatalf("parse: %d cards, err %v", len(caps), err)
	}
	if caps[0].Name != "surface-cap" || len(caps[0].Resources) != 1 {
		t.Fatalf("card mismatch: %+v", caps[0])
	}

	bad := capabilityDoc("surface-cap")
	bad.Format = "memhop-capability/v3"
	data, _ = json.Marshal(bad)
	if _, err := ParseCapabilityPackage(data, "test"); err == nil {
		t.Fatal("a v3 document must be rejected explicitly")
	}
}

func TestValidateCapabilityCard(t *testing.T) {
	card := capabilityDoc("checker").Capabilities[0]
	if err := ValidateCapabilityCard(&card); err != nil {
		t.Fatalf("valid card rejected: %v", err)
	}
	card.Resources = nil
	err := ValidateCapabilityCard(&card)
	if err == nil {
		t.Fatal("a card without resources must be rejected")
	}
	if !strings.Contains(err.Error(), "resource") {
		t.Fatalf("error should name the resource rule: %v", err)
	}
}
