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
)

// writeCapability drops a valid v4 single-card package document and returns
// its path.
func writeCapability(t *testing.T, dir, name string) string {
	t.Helper()
	doc := internal.CapabilityPackageDoc{
		Format: "memhop-capability/v4", Name: name,
		Capabilities: []internal.CapabilityImport{{
			Name: name, Version: "1", Summary: "summarizes", Trigger: "when asked",
			Resources: []ResourceRef{{Type: CapabilitySkill, Name: name, Desc: "call it"}},
		}},
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
	db := openSurfaceDB(t)
	dir := t.TempDir()
	res, err := db.ImportCapability(writeCapability(t, dir, "surface-cap"))
	if err != nil || res == nil || len(res.CreatedIDs) != 1 {
		t.Fatalf("import capability: %v %+v", err, res)
	}
	id := res.CreatedIDs[0]
	if !isHexID(id) {
		t.Fatalf("capability id not hex: %q", id)
	}
	if one, err := db.ListCapabilities(CapabilityListQuery{IDs: []string{id}}); err != nil || len(one) != 1 {
		t.Fatalf("capability by id: %d found, err %v", len(one), err)
	}
	// Re-import byte-identical content is a no-op reporting the stored card.
	again, err := db.ImportCapability(writeCapability(t, dir, "surface-cap"))
	if err != nil || len(again.UpdatedIDs) != 1 || again.UpdatedIDs[0] != id {
		t.Fatalf("re-import must be idempotent: %v %+v", err, again)
	}
	// List filters: status, package and keyword variants.
	active := CapabilityActive
	pkg := "surface-cap"
	for _, q := range []CapabilityListQuery{
		{},
		{Status: &active},
		{Package: &pkg},
		{Keyword: "surface"},
		{Status: &active, Keyword: "no-such-keyword"},
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
	// Activation is a status patch; re-activating an active card is a no-op.
	if _, err := db.UpdateCapability(id, CapabilityPatch{Status: &active}); err != nil {
		t.Fatalf("activate capability: %v", err)
	}
	if err := db.DeleteCapability(id); err != nil {
		t.Fatalf("delete capability: %v", err)
	}
}
