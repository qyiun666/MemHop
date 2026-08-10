// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package config

import (
	"math"

	"github.com/qyiun666/MemHop/internal/common/mherrors"
)

type MemHopConfig struct {
	DBPath      string `json:"db_path"`
	VectorDim   int    `json:"vector_dim"`
	EncoderAddr string `json:"encoder_addr"`
	EmbedModel  string `json:"embed_model"`
	// EncoderTimeoutSecs bounds each embedding HTTP request; 0 means the
	// documented default of 20 seconds.
	EncoderTimeoutSecs int             `json:"encoder_timeout_secs,omitempty"`
	LLM                LlmConfig       `json:"llm"`
	Defaults           *MemHopDefaults `json:"defaults,omitempty"`
}

func (c *MemHopConfig) Validate() error {
	if c == nil {
		return mherrors.NewError(mherrors.ErrConfig, "config is required")
	}
	if c.DBPath == "" {
		return mherrors.NewError(mherrors.ErrConfig, "DBPath is required")
	}
	if c.VectorDim <= 0 || c.VectorDim > math.MaxUint16 {
		return mherrors.NewError(mherrors.ErrConfig, "vector_dim must be in range (0, 65535]")
	}
	if c.EmbedModel == "" {
		return mherrors.NewError(mherrors.ErrConfig, "EmbedModel is required")
	}
	if c.EncoderTimeoutSecs < 0 {
		return mherrors.NewError(mherrors.ErrConfig, "encoder_timeout_secs must be >= 0")
	}
	if c.LLM.APIURL == "" || c.LLM.APIKey == "" || c.LLM.Model == "" {
		return mherrors.NewError(mherrors.ErrConfig, "LLM.APIURL, LLM.APIKey and LLM.Model are required")
	}
	if c.Defaults == nil {
		return nil
	}
	if w := c.Defaults.SearchWeights; w != nil {
		if w.RRFK < 0 || w.ActivationBonus < 0 || w.RecentChatBonus < 0 {
			return mherrors.NewError(mherrors.ErrConfig, "search weights must be >= 0")
		}
	}
	if d := c.Defaults.DecayConfig; d != nil {
		if d.LambdaNode < 0 || d.LambdaEdge < 0 {
			return mherrors.NewError(mherrors.ErrConfig, "decay lambda must be >= 0")
		}
		if !inUnitRange(d.NodeRemoveThreshold) ||
			!inUnitRange(d.NodePruneEdgesThreshold) ||
			!inUnitRange(d.EdgeRemoveThreshold) {
			return mherrors.NewError(mherrors.ErrConfig, "decay thresholds must be in [0, 1]")
		}
	}
	return nil
}

func inUnitRange(v float32) bool { return v >= 0 && v <= 1 }

type MemHopDefaults struct {
	SearchWeights            *SearchWeights `json:"search_weights,omitempty"`
	DecayConfig              *DecayConfig   `json:"decay_config,omitempty"`
	SessionConfig            *SessionConfig `json:"session_config,omitempty"`
	AdjacencyCacheMaxEntries int            `json:"adjacency_cache_max_entries"`
	TokenizerEngine          string         `json:"tokenizer_engine,omitempty"`
}

// SearchWeights controls retrieval scoring.
// Unified RRF pipeline — channel weights and multiplicative boost removed.
type SearchWeights struct {
	RRFK            float32 `json:"rrf_k"`
	ActivationBonus float32 `json:"activation_bonus"`
	RecentChatBonus float32 `json:"recent_chat_bonus"`
}

// LlmConfig holds LLM provider settings.
type LlmConfig struct {
	APIURL          string `json:"api_url"`
	APIKey          string `json:"api_key"`
	Model           string `json:"model"`
	TimeoutSecs     int    `json:"timeout_secs"`
	MaxOutputTokens int    `json:"max_output_tokens"`
}

// DecayConfig controls memory decay parameters.
type DecayConfig struct {
	LambdaNode              float32 `json:"lambda_node"`
	LambdaEdge              float32 `json:"lambda_edge"`
	NodeRemoveThreshold     float32 `json:"node_remove_threshold"`
	NodePruneEdgesThreshold float32 `json:"node_prune_edges_threshold"`
	EdgeRemoveThreshold     float32 `json:"edge_remove_threshold"`
	MinEdgeNodes            int     `json:"min_edge_nodes"`
}

// SessionConfig controls session management.
type SessionConfig struct {
	DefaultTTLMs int64 `json:"default_ttl_ms"`
	Capacity     int   `json:"capacity"`
}

// DefaultMemHopDefaults returns MemHopDefaults with sensible defaults.
func DefaultMemHopDefaults() *MemHopDefaults {
	return &MemHopDefaults{
		AdjacencyCacheMaxEntries: 128,
		TokenizerEngine:          "auto",
		SearchWeights: &SearchWeights{
			RRFK:            60.0,
			ActivationBonus: 0.02,
			RecentChatBonus: 0.01,
		},
		DecayConfig: &DecayConfig{
			LambdaNode:              0.01,
			LambdaEdge:              0.02,
			NodeRemoveThreshold:     0.05,
			NodePruneEdgesThreshold: 0.15,
			EdgeRemoveThreshold:     0.05,
			MinEdgeNodes:            2,
		},
		SessionConfig: &SessionConfig{
			DefaultTTLMs: 3600000, // 1 hour
			Capacity:     7,
		},
	}
}
