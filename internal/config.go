// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The composition root's assembly point: resolving the database path's three
// states, opening or creating the engine, settling the primary domain and
// reloading the tenant registry. The configuration types themselves live in
// internal/config.

package internal

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/llm"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// openEngine resolves the three states a database path can be in. Every branch
// has to be told apart, because creating truncates: only a path that is
// confirmed absent may be created, so any other stat failure surfaces instead
// of falling through to it. A path that is there but is a directory is refused
// here rather than handed to core.Open, which would report it as a file too
// small to hold the dual headers. allowCreate is the caller's decision — an
// open that has nothing to seed a new file with must not leave one behind.
func openEngine(path string, allowCreate bool) (*core.StorageEngine, error) {
	info, err := os.Stat(path)
	switch {
	case err == nil:
		if info.IsDir() {
			return nil, common.NewError(common.ErrInvalidQuery, "database path is a directory: "+path)
		}
		return core.Open(path)
	case errors.Is(err, os.ErrNotExist):
		if !allowCreate {
			return nil, common.NewError(common.ErrConfig,
				"no database at "+path+" and no primary profile to seed one with")
		}
		return core.Create(path)
	default:
		return nil, common.NewError(common.ErrIO, "stat database path", err)
	}
}

// assemble builds the DB around an already-opened engine: the cancellable root
// context, the library-wide transport and the tenant maps reloaded from the
// file's registry records.
func assemble(engine *core.StorageEngine, cfg *MemHopConfig) *DB {
	ctx, cancel := context.WithCancel(context.Background())
	idToName, nameToID, registryErr := loadTenantRegistry(engine)
	if registryErr != nil {
		// One domain's key will not resolve. That costs the file nothing — every
		// name that did resolve keeps working — and the action it does block is
		// creating a tenant, which is refused where it is asked for.
		slog.Warn("memhop: tenant registry carries an unreadable key; creating a sub-agent is refused until it resolves", "err", registryErr)
	}
	return &DB{
		engine: engine,
		config: cfg,
		llm:    llm.New(cfg.LLM),
		// baseCtx bounds every per-agent opCtx; Close cancels it so
		// in-flight Dreams exit at the next stage boundary.
		baseCtx:    ctx,
		baseCancel: cancel,
		agents:     make(map[uint64]*domain.Context),
		nameToID:   nameToID,
		idToName:   idToName,
		llmByAgent: make(map[uint64]*llm.Provider),
		providers:  make(map[LlmConfig]*llm.Provider),
	}
}

// abandon closes a DB that was assembled but cannot be handed out, and reports
// the original failure: the close is cleanup, not the answer, but a close that
// itself fails is joined rather than dropped.
func abandon(db *DB, cause error) error {
	if err := db.Close(); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// OpenDB opens the database at path and settles its primary domain. The primary
// is the implicit zero domain, so a file holds exactly one and finding it costs
// no scan. What happens depends on the file and on that domain's profile:
//
//	file there, profile there    → the file's own profile wins, the argument is ignored
//	file there, no profile       → seed it from the argument; without one, refuse
//	no file                      → create and seed; without a profile, refuse
//
// Both refusals happen before anything touches the filesystem, so a refused open
// leaves no file behind for the next attempt to trip over. AgentType is stamped
// here rather than taken from the caller: the primary domain is the one a file is
// opened on, and a profile cannot claim otherwise.
func OpenDB(path string, llmCfg LlmConfig, defaults MemHopDefaults, primary *core.ProfileSlot) (*DB, error) {
	if path == "" {
		return nil, common.NewError(common.ErrConfig, "path is required")
	}
	if err := llmCfg.Validate(); err != nil {
		return nil, err
	}
	if primary != nil && strings.TrimSpace(primary.Name) == "" {
		return nil, common.NewError(common.ErrInvalidQuery, "primary profile Name is required")
	}
	engine, err := openEngine(path, primary != nil)
	if err != nil {
		return nil, err
	}
	db := assemble(engine, &MemHopConfig{DBPath: path, LLM: llmCfg, Defaults: defaults})
	has, err := repo.HasProfileL0(engine, core.DefaultAgentID)
	if err != nil {
		return nil, abandon(db, err)
	}
	switch {
	case has:
		// The file's own primary is the source of truth; the argument is not
		// consulted, so opening a file never rewrites whose memory it holds.
		return db, nil
	case primary == nil:
		return nil, abandon(db, common.NewError(common.ErrConfig,
			"the database at "+path+" has no primary profile; pass one to seed it"))
	}
	slot := *primary
	slot.Name = strings.TrimSpace(slot.Name)
	slot.AgentType = core.AgentTypePrimary
	slot.UpdatedAtMs = time.Now().UnixMilli()
	if err := repo.UpdateProfileL0(engine, core.DefaultAgentID, &slot); err != nil {
		return nil, abandon(db, err)
	}
	return db, nil
}

// loadTenantRegistry rebuilds the tenant name maps from the on-file
// registry records so ensureRegistered reuses stable IDs across restarts. The
// error it brings back reports a domain whose key will not resolve to a name: the
// file stays open and every name that did resolve keeps working, so this is
// recorded and warned once rather than failed — what it costs is creating a
// tenant.
func loadTenantRegistry(engine *core.StorageEngine) (idToName map[uint64]string, nameToID map[string]uint64, unresolved error) {
	listed, err := repo.ListAgentRegistry(engine)
	idToName = make(map[uint64]string, len(listed))
	nameToID = make(map[string]uint64, len(listed))
	ids := slices.Sorted(maps.Keys(listed))
	for _, id := range ids {
		name := listed[id]
		idToName[id] = name
		// Should two active registry records ever carry the same name, the
		// higher agentID wins deterministically: Go map iteration order must
		// never decide which domain a tenant lands in.
		if prev, ok := nameToID[name]; !ok || id > prev {
			nameToID[name] = id
		}
	}
	return idToName, nameToID, err
}
