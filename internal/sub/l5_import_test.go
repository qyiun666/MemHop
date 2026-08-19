// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package sub

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/sub/repo/core"
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
