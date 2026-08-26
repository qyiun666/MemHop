// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"math"
	"os"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

type MemHopConfig struct {
	DBPath             string         `json:"db_path"`
	VectorDim          int            `json:"vector_dim"`
	EncoderAddr        string         `json:"encoder_addr"`
	EmbedModel         string         `json:"embed_model"`
	EncoderTimeoutSecs int            `json:"encoder_timeout_secs,omitempty"`
	LLM                LlmConfig      `json:"llm"`
	Defaults           MemHopDefaults `json:"defaults"`
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
	if c.VectorDim <= 0 || c.VectorDim > math.MaxUint16 {
		return common.NewError(common.ErrConfig, "vector_dim must be in range (0, 65535]")
	}
	if c.EmbedModel == "" {
		return common.NewError(common.ErrConfig, "EmbedModel is required")
	}
	if c.EncoderTimeoutSecs < 0 {
		return common.NewError(common.ErrConfig, "encoder_timeout_secs must be >= 0")
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
	return core.Create(cfg.DBPath, uint16(cfg.VectorDim))
}

// CheckVectorDim verifies the on-disk vector dimension; on mismatch the
// caller must roll back via CloseNoCheckpoint (an empty snapshot here would
// flip the A/B header and destroy the index snapshot).
func CheckVectorDim(engine *core.StorageEngine, cfg *MemHopConfig) error {
	if int(engine.VectorDim()) != cfg.VectorDim {
		return common.NewError(common.ErrVectorDimMismatch, "config vs engine")
	}
	return nil
}

// LoadCachedIndices restores the sparse index from the checkpoint
// snapshot; a corrupt snapshot aborts Open rather than silently rebuilding.
// L1 association is a storage-level graph walk (SpreadingActivation), so no
// in-memory L1 index is loaded.
// LoadCachedIndices validates every per-agent sparse blob of the snapshot
// and returns them as the lazy-restore cache; a corrupt blob aborts Open
// rather than silently rebuilding.
func LoadCachedIndices(engine *core.StorageEngine) (map[uint64][]byte, error) {
	blobs := make(map[uint64][]byte)
	snap := engine.SnapshotData()
	if snap == nil {
		return blobs, nil
	}
	for agentID, blob := range snap.SparseByAgent {
		if len(blob) == 0 {
			continue
		}
		if _, err := index.DeserializeSparseIndex(blob); err != nil {
			return nil, common.NewError(common.ErrCorruption,
				"sparse index snapshot deserialize failed", err)
		}
		blobs[agentID] = blob
	}
	return blobs, nil
}

// InitTokenizer surfaces tokenizer configuration errors at Open time
// instead of silently degrading to empty tokenization.
func InitTokenizer(engine string) error {
	if err := index.InitTokenizer(engine); err != nil {
		return common.NewError(common.ErrConfig, "tokenizer init failed", err)
	}
	return nil
}

// Open assembles a DB instance: tokenizer init, engine open/create,
// vector-dim check and snapshot validation. Agent contexts are created
// lazily on first access (contextFor), each rebuilding its L2Meta cache
// from its own domain. Called by the internal assembly layer.
func Open(cfg *MemHopConfig, enc Encoder) (*DB, error) {
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
	if err := CheckVectorDim(engine, cfg); err != nil {
		engine.CloseNoCheckpoint()
		cancel()
		return nil, err
	}
	blobs, err := LoadCachedIndices(engine)
	if err != nil {
		engine.CloseNoCheckpoint()
		cancel()
		return nil, err
	}

	// Rebuild the tenant name map from the on-file registry records so
	// CreateAgent reuses stable IDs across restarts.
	idToName := repo.ListAgentRegistry(engine)
	nameToID := make(map[string]uint64, len(idToName))
	for id, name := range idToName {
		nameToID[name] = id
	}

	return &DB{
		engine:  engine,
		config:  cfg,
		llm:     New(cfg),
		encoder: enc,
		// baseCtx bounds every per-agent dreamCtx; Close cancels it so
		// in-flight Dreams exit at the next stage boundary.
		baseCtx:       ctx,
		baseCancel:    cancel,
		agents:        make(map[uint64]*agentContext),
		snapshotBlobs: blobs,
		nameToID:      nameToID,
		idToName:      idToName,
	}, nil
}
