// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 capability CRUD surface tests.

package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// writeCapability drops a valid v3 skill-capability document and returns its path.
func writeCapability(t *testing.T, dir, name string) string {
	t.Helper()
	doc := internal.CapabilityImport{
		Format: "memhop-capability/v3", Name: name, Version: "1",
		Type: core.CapabilitySkill, Summary: "summarizes", Trigger: "when asked",
		Resources: []core.ResourceRef{{Type: core.CapabilitySkill, Name: name, Desc: "call it"}},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSurfaceL5Capability(t *testing.T) {
	db, _ := openSurfaceDB(t)
	dir := t.TempDir()
	cap, err := db.ImportCapability(writeCapability(t, dir, "surface-cap"))
	if err != nil || cap == nil {
		t.Fatalf("import capability: %v", err)
	}
	id := cap.IDHash
	if !isHexID(id) {
		t.Fatalf("capability id not hex: %q", id)
	}
	if _, err := db.GetCapability(id); err != nil {
		t.Fatalf("get capability: %v", err)
	}
	// Re-import byte-identical content is a no-op returning the stored record.
	again, err := db.ImportCapability(writeCapability(t, dir, "surface-cap"))
	if err != nil || again.IDHash != cap.IDHash {
		t.Fatalf("re-import must be idempotent: %v %+v", err, again)
	}
	// List filters: status, type and keyword variants.
	active := core.CapabilityActive
	skill := core.CapabilitySkill
	for _, q := range []CapabilityListQuery{
		{},
		{Status: &active},
		{Type: &skill},
		{Keyword: "surface"},
		{Status: &active, Type: &skill, Keyword: "no-such-keyword"},
	} {
		list, err := db.ListCapabilities(q)
		if err != nil || list == nil {
			t.Fatalf("list capabilities %+v: %v", q, err)
		}
	}
	// Patch a mutable field and verify round-trip.
	newSum := "updated summary"
	updated, err := db.UpdateCapability(id, CapabilityPatch{Summary: &newSum})
	if err != nil || updated.Summary != newSum {
		t.Fatalf("update capability: %v %+v", err, updated)
	}
	if _, err := db.RecordCapabilityUsage(id, true); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	// Activate is idempotent on an already-active imported capability.
	if _, err := db.ActivateCapability(id); err != nil {
		t.Fatalf("activate capability: %v", err)
	}
	// Built-in capabilities are read-only: patching one must be rejected.
	builtins, _ := db.ListCapabilities(CapabilityListQuery{})
	var builtinID string
	for _, b := range builtins {
		if b.Origin == core.CapabilityOriginBuiltin {
			builtinID = b.IDHash
			break
		}
	}
	if builtinID != "" {
		s := "x"
		if _, err := db.UpdateCapability(builtinID, CapabilityPatch{Summary: &s}); CodeOf(err) != ErrInvalidQuery {
			t.Fatalf("patch builtin must be rejected: got %v", err)
		}
	}
	if err := db.DeleteCapability(id); err != nil {
		t.Fatalf("delete capability: %v", err)
	}
}
