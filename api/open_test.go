// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal"
)

func openTestConfig(dbPath string) *internal.MemHopConfig {
	return &internal.MemHopConfig{
		DBPath: dbPath,
		LLM: internal.LlmConfig{
			APIURL: "http://127.0.0.1:1", APIKey: "k", Model: "m",
		},
		Defaults: *internal.DefaultMemHopDefaults,
	}
}

var builtinNames = []string{
	"memhop-guide", "memhop-knowledge", "memhop-scene", "memhop-archive",
	"memhop-profile", "memhop-capability",
}

// OpenMulti attaches the toolbox in memory: ListCapabilities/GetCapability
// serve it immediately, close/reopen stays clean, and nothing is persisted
// (storage-only views are verified at the internal layer). The
// loadBuiltinCapabilities shape check moved to the internal package with the
// function itself (api is a pure forwarding facade).
func TestOpenAttachesBuiltins(t *testing.T) {
	cfg := openTestConfig(filepath.Join(t.TempDir(), "b.meh"))
	m, err := OpenMulti(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	id, err := m.CreateAgent("test")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	sess, err := m.Session(id)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	caps, err := sess.ListCapabilities(internal.CapabilityListQuery{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := len(builtinNames)
	if len(caps) != want {
		t.Fatalf("want %d builtin capabilities served, got %d", want, len(caps))
	}
	for _, c := range caps {
		if c.Origin != CapabilityOriginBuiltin {
			t.Fatalf("fresh DB must serve only builtins: %+v", c)
		}
	}
	if err := m.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	m2, err := OpenMulti(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer m2.Close()
}
