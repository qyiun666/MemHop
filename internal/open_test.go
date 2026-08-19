// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/sub"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

type openTestEncoder struct{ dim int }

func (m *openTestEncoder) Encode(string) ([]float32, error) { return make([]float32, m.dim), nil }
func (m *openTestEncoder) IsAvailable() bool                { return true }

func openTestConfig(dbPath string) *sub.MemHopConfig {
	return &sub.MemHopConfig{
		DBPath:     dbPath,
		VectorDim:  4,
		EmbedModel: "test-embed",
		LLM: sub.LlmConfig{
			APIURL: "http://127.0.0.1:1", APIKey: "k", Model: "m",
		},
		Defaults: *sub.DefaultMemHopDefaults,
	}
}

var builtinManualNames = []string{
	"memhop-guide", "memhop-search", "memhop-update", "memhop-dream",
	"memhop-trajectory", "memhop-crystallize", "memhop-capability-import",
}

var builtinAtomicNames = []string{
	"agent-read-file", "agent-write-file", "agent-edit-file",
	"agent-run-command", "agent-search-files", "agent-web-search",
}

// The embedded toolbox must parse into valid active capabilities with
// stable name-derived IDs; a corrupted embedded file fails here.
func TestLoadBuiltinCapabilities(t *testing.T) {
	caps, err := loadBuiltinCapabilities()
	if err != nil {
		t.Fatalf("loadBuiltinCapabilities: %v", err)
	}
	want := len(builtinManualNames) + len(builtinAtomicNames)
	if len(caps) != want {
		t.Fatalf("want %d builtin capabilities, got %d", want, len(caps))
	}
	seen := map[string]struct{}{}
	for _, c := range caps {
		seen[c.Name] = struct{}{}
		if c.Status != core.CapabilityActive || c.Origin != core.CapabilityOriginBuiltin {
			t.Fatalf("builtin %s: status=%v origin=%v", c.Name, c.Status, c.Origin)
		}
		if c.IDHash != core.CapabilityID(c.Name) {
			t.Fatalf("builtin %s: unstable ID %d", c.Name, c.IDHash)
		}
		if c.FileHash == "" {
			t.Fatalf("builtin %s: missing file hash", c.Name)
		}
		switch c.Type {
		case core.CapabilityMCP, core.CapabilitySkill:
			if len(c.Resources) != 1 || c.Resources[0].Type != c.Type {
				t.Fatalf("builtin %s: resources mismatch %+v", c.Name, c)
			}
		case core.CapabilityComposite:
			if len(c.Resources) == 0 {
				t.Fatalf("builtin composite %s: no resources %+v", c.Name, c)
			}
		default:
			t.Fatalf("builtin %s: unexpected type %v", c.Name, c.Type)
		}
	}
	for _, name := range append(append([]string{}, builtinManualNames...), builtinAtomicNames...) {
		if _, ok := seen[name]; !ok {
			t.Fatalf("builtin %s missing", name)
		}
	}
}

// Open attaches the toolbox in memory: ListCapabilities/GetCapability serve
// it immediately, close/reopen stays clean, and nothing is persisted
// (storage-only views are verified at the sub layer).
func TestOpenAttachesBuiltins(t *testing.T) {
	cfg := openTestConfig(filepath.Join(t.TempDir(), "b.meh"))
	db, err := OpenWithEncoder(cfg, &openTestEncoder{dim: 4})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	caps, err := db.ListCapabilities(sub.CapabilityListQuery{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := len(builtinManualNames) + len(builtinAtomicNames)
	if len(caps) != want {
		t.Fatalf("want %d builtin capabilities served, got %d", want, len(caps))
	}
	for _, c := range caps {
		if c.Origin != core.CapabilityOriginBuiltin {
			t.Fatalf("fresh DB must serve only builtins: %+v", c)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	db2, err := OpenWithEncoder(cfg, &openTestEncoder{dim: 4})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
}
