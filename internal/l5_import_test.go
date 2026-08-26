// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

const testCapabilityJSON = `{
  "format": "memhop-capability/v2",
  "name": "测试工具",
  "type": "mcp",
  "summary": "测试用 mcp 封装能力",
  "trigger": "测试触发",
  "resources": [
    {"type": "mcp", "name": "test_tool", "ref": "test-server", "description": "测试用"}
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

// An api-typed capability (ref api:MethodName) imports like any other:
// exactly one same-typed resource is required, more are rejected.
func TestImportCapabilityAPIType(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}

	apiPath := writeTempCapability(t, t.TempDir(), "cap.json", `{
  "format": "memhop-capability/v2",
  "name": "api-测试卡",
  "type": "api",
  "summary": "封装一个 api 方法",
  "trigger": "api 测试",
  "resources": [
    {"type": "api", "name": "GetL0", "ref": "api:GetL0", "description": "读取画像"}
  ]
}`)
	cap, err := db.ImportCapability(apiPath)
	if err != nil {
		t.Fatalf("import api capability: %v", err)
	}
	if cap.Type != core.CapabilityAPI || len(cap.Resources) != 1 || cap.Resources[0].Ref != "api:GetL0" {
		t.Fatalf("api capability mismatch: %+v", cap)
	}

	badPath := writeTempCapability(t, t.TempDir(), "bad.json", `{
  "format": "memhop-capability/v2",
  "name": "api-坏卡",
  "type": "api",
  "summary": "两个资源应被拒绝",
  "trigger": "api 测试",
  "resources": [
    {"type": "api", "name": "GetL0", "ref": "api:GetL0", "description": "a"},
    {"type": "api", "name": "UpdateL0", "ref": "api:UpdateL0", "description": "b"}
  ]
}`)
	if _, err := db.ImportCapability(badPath); err == nil {
		t.Fatal("api capability with two resources must be rejected")
	}
}

// Byte-identical re-import under the same name must not append a record:
// the file is append-only and hosts may re-import at every startup.
func TestImportCapabilityUnchangedSkip(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	path := writeTempCapability(t, t.TempDir(), "cap.json", testCapabilityJSON)

	first, err := db.ImportCapability(path)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if first.Status != core.CapabilityActive || first.Origin != core.CapabilityOriginImported {
		t.Fatalf("first import: status=%v origin=%v", first.Status, first.Origin)
	}
	second, err := db.ImportCapability(path)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.UpdatedAt != first.UpdatedAt {
		t.Fatalf("unchanged re-import must be a no-op: UpdatedAt %d -> %d", first.UpdatedAt, second.UpdatedAt)
	}
	if got := len(core.CollectAllCapabilities(db.engine)); got != 1 {
		t.Fatalf("unchanged re-import appended a record: %d capabilities", got)
	}
}
