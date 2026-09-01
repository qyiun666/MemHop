// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"io/fs"
	"maps"
	"os"
	"slices"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// MemHopConfig configures a MemHop database. The only external service is the
// LLM endpoint: the retrieval subsystem that used to consume encoded vectors
// is gone, so no embedding service is contacted and no dimension is declared.
type MemHopConfig struct {
	DBPath   string         `json:"db_path"`
	LLM      LlmConfig      `json:"llm"`
	Defaults MemHopDefaults `json:"defaults"`
}

// LlmConfig holds LLM provider settings.
type LlmConfig struct {
	APIURL          string `json:"api_url"`
	APIKey          string `json:"api_key"`
	Model           string `json:"model"`
	TimeoutSecs     int    `json:"timeout_secs"`
	MaxOutputTokens int    `json:"max_output_tokens"`
}

func (c *MemHopConfig) Validate() error {
	if c == nil {
		return common.NewError(common.ErrConfig, "config is required")
	}
	if c.DBPath == "" {
		return common.NewError(common.ErrConfig, "DBPath is required")
	}
	if c.LLM.APIURL == "" || c.LLM.APIKey == "" || c.LLM.Model == "" {
		return common.NewError(common.ErrConfig, "LLM.APIURL, LLM.APIKey and LLM.Model are required")
	}
	return nil
}

func OpenOrCreateEngine(cfg *MemHopConfig) (*core.StorageEngine, error) {
	if _, err := os.Stat(cfg.DBPath); err == nil {
		return core.Open(cfg.DBPath)
	}
	return core.Create(cfg.DBPath)
}

// InitTokenizer surfaces tokenizer configuration errors at Open time
// instead of silently degrading to empty tokenization. The tokenizer's one
// live consumer is the keyword-extraction heuristic fallback.
func InitTokenizer(engine string) error {
	if err := index.InitTokenizer(engine); err != nil {
		return common.NewError(common.ErrConfig, "tokenizer init failed", err)
	}
	return nil
}

// Open assembles a DB instance: tokenizer init, engine open/create and
// attachment of the built-in capability toolbox injected as an fs.FS by the
// facade (internal must not import the capabilities package). Agent contexts
// are created lazily on first access (contextFor), each rebuilding its caches
// from its own domain's records.
func Open(cfg *MemHopConfig, builtins fs.FS) (*DB, error) {
	ctx, cancel := context.WithCancel(context.Background())
	if err := InitTokenizer(defaultTokenizerEngine); err != nil {
		cancel()
		return nil, err
	}
	engine, err := OpenOrCreateEngine(cfg)
	if err != nil {
		cancel()
		return nil, err
	}

	idToName, nameToID := loadTenantRegistry(engine)

	db := &DB{
		engine: engine,
		config: cfg,
		llm:    New(cfg),
		// baseCtx bounds every per-agent opCtx; Close cancels it so
		// in-flight Dreams exit at the next stage boundary.
		baseCtx:    ctx,
		baseCancel: cancel,
		agents:     make(map[uint64]*agentContext),
		nameToID:   nameToID,
		idToName:   idToName,
	}
	// Attach the injected built-in capability manuals: read-only reference
	// cards served by the L5 read APIs, never written into the .meh file.
	builtinCaps, err := loadBuiltinCapabilities(builtins)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	db.SetBuiltinCapabilities(builtinCaps)
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
