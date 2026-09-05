// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

// injectPlugDir scans <dir(meh)>/plug/<package>/capability.json into the
// shared L5 pool: good packages land active and import-stamped, a broken
// package is skipped with a warning instead of failing Open, loose files are
// ignored, and a re-scan of unchanged bytes writes nothing.
func TestInjectPlugDir(t *testing.T) {
	dir := t.TempDir()
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	db.config.DBPath = filepath.Join(dir, "test.meh")

	plugDir := filepath.Join(dir, "plug")
	goodDir := filepath.Join(plugDir, "good")
	if err := os.MkdirAll(goodDir, 0o755); err != nil {
		t.Fatal(err)
	}
	good := `{
  "format": "memhop-capability/v4",
  "name": "good",
  "capabilities": [
    {"name": "插件卡", "summary": "来自 plug 目录", "trigger": "插件",
     "resources": [{"type": "mcp", "name": "plug_tool", "ref": "plug-server"}]}
  ]
}`
	if err := os.WriteFile(filepath.Join(goodDir, "capability.json"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	badDir := filepath.Join(plugDir, "bad")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "capability.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugDir, "loose.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	db.injectPlugDir(db.config.DBPath)
	caps, err := db.ListCapabilities(core.DefaultAgentID, CapabilityListQuery{})
	if err != nil {
		t.Fatalf("list after inject: %v", err)
	}
	if len(caps) != 1 || caps[0].Name != "插件卡" || caps[0].Package != "good" ||
		caps[0].Origin != core.CapabilityOriginImported || caps[0].Status != core.CapabilityActive {
		t.Fatalf("injected cards = %+v", caps)
	}

	// A re-scan of unchanged bytes is a no-op: the append-only file must not
	// grow on every Open.
	db.injectPlugDir(db.config.DBPath)
	if got := len(core.CollectAllCapabilities(engine, core.SharedPoolAgentID)); got != 1 {
		t.Fatalf("re-scan appended records: %d capabilities", got)
	}
}
