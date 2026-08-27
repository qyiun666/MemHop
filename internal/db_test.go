// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// newTestDB wires an engine into the minimal multi-agent DB state the
// domain-context machinery needs (registry, base context, defaults),
// mirroring the Open assembly. The default-domain context is created
// lazily by contextFor on first use.
func newTestDB(t *testing.T, engine *core.StorageEngine) *DB {
	t.Helper()
	baseCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &DB{
		engine:        engine,
		config:        &MemHopConfig{Defaults: *DefaultMemHopDefaults},
		baseCtx:       baseCtx,
		baseCancel:    cancel,
		agents:        make(map[uint64]*agentContext),
		snapshotBlobs: make(map[uint64][]byte),
	}
}

// testDefaultContext returns the default-domain context of db, creating it
// with fresh indexes when absent (same shape as the lazy contextFor path);
// tests that poke ac.l2Meta / ac.activeScenes directly use this handle.
func testDefaultContext(db *DB) *agentContext {
	if ac := db.agents[core.DefaultAgentID]; ac != nil {
		return ac
	}
	ac := newAgentContext(core.DefaultAgentID, db.baseCtx)
	ac.sparseIndex = index.NewSparseIndex()
	ac.l2Meta = index.BuildL2MetaFromEngine(db.engine, core.DefaultAgentID)
	db.agents[core.DefaultAgentID] = ac
	return ac
}

// TestLoadTenantRegistryDuplicateNameDeterministic pins the restart mapping
// rule: two active registry records carrying the same name resolve to the
// highest agentID regardless of Go map iteration order.
func TestLoadTenantRegistryDuplicateNameDeterministic(t *testing.T) {
	engine := newTestEngine(t)
	const name = "dup"
	low, high := uint64(0x1111111111111111), uint64(0x2222222222222222)
	for _, id := range []uint64{low, high} {
		if err := repo.WriteAgentRegistry(engine, id, name); err != nil {
			t.Fatalf("write registry record %d: %v", id, err)
		}
	}
	idToName, nameToID := loadTenantRegistry(engine)
	if got := nameToID[name]; got != high {
		t.Fatalf("duplicate name resolves to %#x, want higher ID %#x", got, high)
	}
	if idToName[low] != name || idToName[high] != name {
		t.Fatalf("idToName missing entries: %q / %q", idToName[low], idToName[high])
	}
}
