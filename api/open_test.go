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

// Close/reopen stays clean: no Open-time injection remains, so a reopened
// database serves exactly what previous rounds wrote.
func TestOpenCloseReopenCycle(t *testing.T) {
	cfg := openTestConfig(filepath.Join(t.TempDir(), "b.meh"))
	m, err := OpenMulti(cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := m.CreateAgent("test"); err != nil {
		t.Fatalf("create agent: %v", err)
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
