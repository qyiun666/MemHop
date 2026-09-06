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

// A fresh database starts with an empty L5 pool: capabilities only exist
// when a host imports them (or plug/ packages inject at Open), and
// close/reopen stays clean.
func TestOpenFreshPoolEmpty(t *testing.T) {
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
	if len(caps) != 0 {
		t.Fatalf("fresh DB must serve an empty pool, got %+v", caps)
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
