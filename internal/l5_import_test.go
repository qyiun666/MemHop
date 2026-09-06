// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

const testCapabilityJSON = `{
  "format": "memhop-capability/v4",
  "name": "测试包",
  "capabilities": [
    {
      "name": "测试工具",
      "summary": "测试用 mcp 封装能力",
      "trigger": "测试触发",
      "resources": [
        {"type": "mcp", "name": "test_tool", "ref": "test-server", "desc": "测试用"}
      ]
    }
  ]
}`

func writeTempCapability(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp capability: %v", err)
	}
	return p
}

// A v4 package imports every card into the shared pool; api-typed entries
// (ref api:MethodName) need no special casing, and the package name stamps
// each card.
func TestImportCapabilityPackage(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))

	apiPath := writeTempCapability(t, t.TempDir(), "cap.json", `{
  "format": "memhop-capability/v4",
  "name": "api-测试包",
  "capabilities": [
    {"name": "api-测试卡", "summary": "封装一个 api 方法", "trigger": "api 测试",
     "resources": [{"type": "api", "name": "GetL0", "ref": "api:GetL0", "desc": "读取画像"}]},
    {"name": "api-第二卡", "summary": "同包第二张卡", "trigger": "api 测试",
     "resources": [{"type": "api", "name": "UpdateL0", "ref": "api:UpdateL0", "desc": "b"}]}
  ]
}`)
	result, err := db.ImportCapability(core.DefaultAgentID, apiPath)
	if err != nil {
		t.Fatalf("import package: %v", err)
	}
	if len(result.CreatedIDs) != 2 || len(result.UpdatedIDs) != 0 || len(result.Errors) != 0 {
		t.Fatalf("package import result: %+v", result)
	}
	pkg := "api-测试包"
	caps, err := db.ListCapabilities(core.DefaultAgentID, CapabilityListQuery{Package: &pkg})
	if err != nil {
		t.Fatalf("list by package: %v", err)
	}
	if len(caps) != 2 || caps[0].Package != "api-测试包" {
		t.Fatalf("package cards mismatch: %+v", caps)
	}

	// A pre-v4 document is rejected explicitly, not silently coerced.
	v3Path := writeTempCapability(t, t.TempDir(), "old.json", `{
  "format": "memhop-capability/v3",
  "name": "旧卡",
  "type": "api",
  "summary": "s",
  "trigger": "t",
  "resources": [{"type": "api", "name": "GetL0", "ref": "api:GetL0"}]
}`)
	if _, err := db.ImportCapability(core.DefaultAgentID, v3Path); err == nil {
		t.Fatal("v3 document must be rejected")
	}
}

// Byte-identical re-import under the same name must not append a record:
// the file is append-only and the plug/ scan re-imports at every startup.
func TestImportCapabilityUnchangedSkip(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	path := writeTempCapability(t, t.TempDir(), "cap.json", testCapabilityJSON)

	first, err := db.ImportCapability(core.DefaultAgentID, path)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if len(first.CreatedIDs) != 1 {
		t.Fatalf("first import: %+v", first)
	}
	stored, err := repo.GetCapabilityL5(db.engine, core.SharedPoolAgentID, core.CapabilityID("测试工具"))
	if err != nil {
		t.Fatalf("read stored: %v", err)
	}
	second, err := db.ImportCapability(core.DefaultAgentID, path)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if len(second.UpdatedIDs) != 1 || len(second.CreatedIDs) != 0 {
		t.Fatalf("unchanged re-import must be a no-op update: %+v", second)
	}
	after, err := repo.GetCapabilityL5(db.engine, core.SharedPoolAgentID, core.CapabilityID("测试工具"))
	if err != nil {
		t.Fatalf("read stored after: %v", err)
	}
	if after.UpdatedAt != stored.UpdatedAt {
		t.Fatalf("unchanged re-import must not touch the record: %d -> %d", stored.UpdatedAt, after.UpdatedAt)
	}
	if got := len(core.CollectAllCapabilities(db.engine, core.SharedPoolAgentID)); got != 1 {
		t.Fatalf("unchanged re-import appended a record: %d capabilities", got)
	}
}

// A host edit (deprecation, patched trigger) must survive the byte-identical
// re-import the plug/ scan performs on every Open: FileHash is the package
// watermark, so the unchanged package hits the skip guard instead of
// upserting a fresh active copy over the host's decision.
func TestImportCapabilityHostEditsSurviveReimport(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	path := writeTempCapability(t, t.TempDir(), "cap.json", testCapabilityJSON)

	if _, err := db.ImportCapability(core.DefaultAgentID, path); err != nil {
		t.Fatalf("first import: %v", err)
	}
	id := common.FormatHash(core.CapabilityID("测试工具"))
	patched := "宿主改过的触发词"
	deprecated := core.CapabilityDeprecated
	if _, err := db.UpdateCapability(core.DefaultAgentID, id, CapabilityPatch{Trigger: &patched, Status: &deprecated}); err != nil {
		t.Fatalf("host update: %v", err)
	}
	stored, err := repo.GetCapabilityL5(db.engine, core.SharedPoolAgentID, core.CapabilityID("测试工具"))
	if err != nil {
		t.Fatalf("read stored: %v", err)
	}

	if _, err := db.ImportCapability(core.DefaultAgentID, path); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	after, err := repo.GetCapabilityL5(db.engine, core.SharedPoolAgentID, core.CapabilityID("测试工具"))
	if err != nil {
		t.Fatalf("read stored after re-import: %v", err)
	}
	if after.Status != core.CapabilityDeprecated || after.Trigger != patched {
		t.Fatalf("host edits must survive re-import, got %+v", after)
	}
	if after.UpdatedAt != stored.UpdatedAt {
		t.Fatalf("unchanged re-import must not touch the record: %d -> %d", stored.UpdatedAt, after.UpdatedAt)
	}
}

// A card naming a built-in manual stores a shadow: the stored copy wins in
// listings (it carries usage statistics), and deleting it hands the listing
// back to the built-in card.
func TestImportCapabilityShadowsBuiltinName(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	db.SetBuiltinCapabilities(testBuiltinCapabilities())
	path := writeTempCapability(t, t.TempDir(), "cap.json", `{
  "format": "memhop-capability/v4",
  "name": "影子包",
  "capabilities": [
    {"name": "内置手册", "summary": "冒充内置卡", "trigger": "t",
     "resources": [{"type": "api", "name": "GetL0", "ref": "api:GetL0"}]}
  ]
}`)
	result, err := db.ImportCapability(core.DefaultAgentID, path)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(result.CreatedIDs) != 1 {
		t.Fatalf("shadow card must be stored: %+v", result)
	}
	listed, err := db.ListCapabilities(core.DefaultAgentID, CapabilityListQuery{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	shadows := 0
	for _, c := range listed {
		if c.IDHash == core.CapabilityID("内置手册") {
			shadows++
			if c.Origin != core.CapabilityOriginImported {
				t.Fatalf("the stored copy must win the listing: %+v", c)
			}
		}
	}
	if shadows != 1 {
		t.Fatalf("a stored shadow must suppress its built-in twin, got %d entries", shadows)
	}
	if err := db.DeleteCapability(core.DefaultAgentID, result.CreatedIDs[0]); err != nil {
		t.Fatalf("delete shadow: %v", err)
	}
	listed, err = db.ListCapabilities(core.DefaultAgentID, CapabilityListQuery{})
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	for _, c := range listed {
		if c.IDHash == core.CapabilityID("内置手册") && c.Origin != core.CapabilityOriginBuiltin {
			t.Fatalf("after deleting the shadow the builtin must resurface: %+v", c)
		}
	}
}
