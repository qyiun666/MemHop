// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Open is the composition root's assembly point: tokenizer init, engine
// open/create, tenant registry reload and the built-in capability toolbox
// attachment. The configuration types themselves live in internal/config.

package internal

import (
	"context"
	"maps"
	"os"
	"slices"

	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/llm"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func OpenOrCreateEngine(cfg *MemHopConfig) (*core.StorageEngine, error) {
	if _, err := os.Stat(cfg.DBPath); err == nil {
		return core.Open(cfg.DBPath)
	}
	return core.Create(cfg.DBPath)
}

// Open assembles a DB instance: tokenizer init and engine open/create.
// Agent contexts are created lazily on first access (contextFor), each
// rebuilding its caches from its own domain's records. After assembly the
// <meh dir>/plug plugin packages are injected into the shared L5 pool
// (l5plug.go).
func Open(cfg *MemHopConfig) (*DB, error) {
	ctx, cancel := context.WithCancel(context.Background())
	engine, err := OpenOrCreateEngine(cfg)
	if err != nil {
		cancel()
		return nil, err
	}

	idToName, nameToID := loadTenantRegistry(engine)

	db := &DB{
		engine: engine,
		config: cfg,
		llm:    llm.New(cfg),
		// baseCtx bounds every per-agent opCtx; Close cancels it so
		// in-flight Dreams exit at the next stage boundary.
		baseCtx:    ctx,
		baseCancel: cancel,
		agents:     make(map[uint64]*domain.Context),
		nameToID:   nameToID,
		idToName:   idToName,
	}
	db.injectPlugDir(cfg.DBPath)
	return db, nil
}

// loadTenantRegistry rebuilds the tenant name maps from the on-file
// registry records so CreateAgent reuses stable IDs across restarts.
func loadTenantRegistry(engine *core.StorageEngine) (idToName map[uint64]string, nameToID map[string]uint64) {
	listed := repo.ListAgentRegistry(engine)
	idToName = make(map[uint64]string, len(listed))
	nameToID = make(map[string]uint64, len(listed))
	ids := slices.Sorted(maps.Keys(listed))
	for _, id := range ids {
		name := listed[id]
		idToName[id] = name
		// Two active registry records carrying the same name (possible after
		// a failed DeleteAgent plus a concurrent same-name CreateAgent)
		// resolve to the highest agentID deterministically: Go map iteration
		// order must never decide which domain a tenant lands in.
		if prev, ok := nameToID[name]; !ok || id > prev {
			nameToID[name] = id
		}
	}
	return idToName, nameToID
}
