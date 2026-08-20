// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"math"
	"os"

	"github.com/qyiun666/MemHop/internal/common"
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
	if c.Defaults.RRFK < 0 || c.Defaults.ActivationBonus < 0 || c.Defaults.RecentChatBonus < 0 {
		return common.NewError(common.ErrConfig, "search weights must be >= 0")
	}
	if c.Defaults.LambdaNode < 0 || c.Defaults.LambdaEdge < 0 {
		return common.NewError(common.ErrConfig, "decay lambda must be >= 0")
	}
	if !inUnitRange(c.Defaults.NodeRemoveThreshold) ||
		!inUnitRange(c.Defaults.NodePruneEdgesThreshold) ||
		!inUnitRange(c.Defaults.EdgeRemoveThreshold) {
		return common.NewError(common.ErrConfig, "decay thresholds must be in [0, 1]")
	}
	return nil
}

func inUnitRange(v float32) bool { return v >= 0 && v <= 1 }

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

// LoadCachedIndices restores the sparse and L1 reverse indices from the
// checkpoint snapshot; a corrupt snapshot aborts Open rather than silently
// rebuilding.
func LoadCachedIndices(engine *core.StorageEngine) (*index.SparseIndex, *index.L1ReverseIndex, error) {
	sparseIdx := index.NewSparseIndex()
	l1Rev := index.NewL1ReverseIndex()
	snap := engine.SnapshotData()
	if snap == nil {
		return sparseIdx, l1Rev, nil
	}
	if len(snap.SparseData) > 0 {
		idx, err := index.DeserializeSparseIndex(snap.SparseData)
		if err != nil {
			return nil, nil, common.NewError(common.ErrCorruption,
				"sparse index snapshot deserialize failed", err)
		}
		sparseIdx = idx
	}
	if len(snap.L1ReverseData) > 0 {
		idx, err := index.DeserializeL1ReverseIndex(snap.L1ReverseData)
		if err != nil {
			return nil, nil, common.NewError(common.ErrCorruption,
				"l1 reverse index snapshot deserialize failed", err)
		}
		l1Rev = idx
	}
	return sparseIdx, l1Rev, nil
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
// vector-dim check and index restore. Called by the internal assembly layer.
func Open(cfg *MemHopConfig, enc Encoder) (*DB, error) {
	if err := InitTokenizer(cfg.Defaults.TokenizerEngine); err != nil {
		return nil, err
	}
	engine, err := OpenOrCreateEngine(cfg)
	if err != nil {
		return nil, err
	}
	if err := CheckVectorDim(engine, cfg); err != nil {
		engine.CloseNoCheckpoint()
		return nil, err
	}
	sparseIdx, l1Rev, err := LoadCachedIndices(engine)
	if err != nil {
		engine.CloseNoCheckpoint()
		return nil, err
	}

	db := &DB{
		engine:      engine,
		config:      cfg,
		llm:         New(cfg),
		sparseIndex: sparseIdx,
		encoder:     enc,
	}
	db.l1Reverse.Store(l1Rev)
	// L2Meta is not snapshot-persisted (snapshot format is fixed), so it is
	// rebuilt once at Open with a single RecL2Topic scan; after that all
	// candidate listing serves from memory.
	db.l2Meta = index.BuildL2MetaFromEngine(engine)
	return db, nil
}
