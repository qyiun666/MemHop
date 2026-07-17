// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package config

import (
	"errors"
	"math"
	"testing"

	"memhop/internal/common/mherrors"
)

func TestValidateNilConfig(t *testing.T) {
	err := (*MemHopConfig)(nil).Validate()
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if !errors.Is(err, mherrors.ErrConfig) {
		t.Errorf("error should wrap ErrConfig")
	}
}

func TestValidateVectorDim(t *testing.T) {
	tests := []struct {
		name string
		dim  int
		want string // non-empty if error expected
	}{
		{"zero", 0, "vector_dim must be in range"},
		{"negative", -1, "vector_dim must be in range"},
		{"exceeds max", math.MaxUint16 + 1, "vector_dim must be in range"},
		{"valid min", 1, ""},
		{"valid typical", 768, ""},
		{"valid max", math.MaxUint16, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &MemHopConfig{VectorDim: tt.dim}
			err := c.Validate()
			if tt.want == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !errors.Is(err, mherrors.ErrConfig) {
					t.Errorf("error should wrap ErrConfig")
				}
			}
		})
	}
}

func TestValidateDefaultsNilReturnsNil(t *testing.T) {
	c := &MemHopConfig{VectorDim: 768}
	if err := c.Validate(); err != nil {
		t.Fatalf("expected nil when Defaults is nil, got %v", err)
	}
}

func TestValidateNegativeSearchWeights(t *testing.T) {
	tests := []struct {
		name  string
		setup func() *SearchWeights
	}{
		{"negative BM25Weight", func() *SearchWeights { return &SearchWeights{BM25Weight: -1, VectorWeight: 0.5, RRFK: 1, EntityWeight: 1, ActivationBonus: 0.1, RecentChatBonus: 0.1, ActivationBoost: 1} }},
		{"negative VectorWeight", func() *SearchWeights { return &SearchWeights{BM25Weight: 0.5, VectorWeight: -1, RRFK: 1, EntityWeight: 1, ActivationBonus: 0.1, RecentChatBonus: 0.1, ActivationBoost: 1} }},
		{"negative RRFK", func() *SearchWeights { return &SearchWeights{BM25Weight: 0.5, VectorWeight: 0.5, RRFK: -1, EntityWeight: 1, ActivationBonus: 0.1, RecentChatBonus: 0.1, ActivationBoost: 1} }},
		{"negative ActivationBonus", func() *SearchWeights { return &SearchWeights{BM25Weight: 0.5, VectorWeight: 0.5, RRFK: 1, EntityWeight: 1, ActivationBonus: -0.1, RecentChatBonus: 0.1, ActivationBoost: 1} }},
		{"negative ActivationBoost", func() *SearchWeights { return &SearchWeights{BM25Weight: 0.5, VectorWeight: 0.5, RRFK: 1, EntityWeight: 1, ActivationBonus: 0.1, RecentChatBonus: 0.1, ActivationBoost: -1} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &MemHopConfig{
				VectorDim: 768,
				Defaults: &MemHopDefaults{
					SearchWeights: tt.setup(),
				},
			}
			err := c.Validate()
			if err == nil {
				t.Fatal("expected error for negative search weight")
			}
		})
	}
}

func TestValidateValidSearchWeights(t *testing.T) {
	c := &MemHopConfig{
		VectorDim: 768,
		Defaults: &MemHopDefaults{
			SearchWeights: &SearchWeights{
				BM25Weight: 0.5, VectorWeight: 0.5, RRFK: 60,
				EntityWeight: 1, ActivationBonus: 0.1,
				RecentChatBonus: 0.05, ActivationBoost: 1.3,
			},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDecayConfig(t *testing.T) {
	tests := []struct {
		name    string
		decay   *DecayConfig
		wantErr bool
	}{
		{"negative lambda_node", &DecayConfig{LambdaNode: -0.01, LambdaEdge: 0.02, NodeRemoveThreshold: 0.05, NodePruneEdgesThreshold: 0.15, EdgeRemoveThreshold: 0.05}, true},
		{"negative lambda_edge", &DecayConfig{LambdaNode: 0.01, LambdaEdge: -0.02, NodeRemoveThreshold: 0.05, NodePruneEdgesThreshold: 0.15, EdgeRemoveThreshold: 0.05}, true},
		{"threshold > 1", &DecayConfig{LambdaNode: 0.01, LambdaEdge: 0.02, NodeRemoveThreshold: 1.5, NodePruneEdgesThreshold: 0.15, EdgeRemoveThreshold: 0.05}, true},
		{"threshold < 0", &DecayConfig{LambdaNode: 0.01, LambdaEdge: 0.02, NodeRemoveThreshold: -0.1, NodePruneEdgesThreshold: 0.15, EdgeRemoveThreshold: 0.05}, true},
		{"valid", &DecayConfig{LambdaNode: 0.01, LambdaEdge: 0.02, NodeRemoveThreshold: 0.05, NodePruneEdgesThreshold: 0.15, EdgeRemoveThreshold: 0.05}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &MemHopConfig{
				VectorDim: 768,
				Defaults: &MemHopDefaults{
					DecayConfig: tt.decay,
				},
			}
			err := c.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDefaultMemHopDefaults(t *testing.T) {
	d := DefaultMemHopDefaults()
	if d == nil {
		t.Fatal("DefaultMemHopDefaults() should not return nil")
	}

	t.Run("search weights defaults", func(t *testing.T) {
		if d.SearchWeights == nil {
			t.Fatal("SearchWeights should not be nil")
		}
		if d.SearchWeights.BM25Weight != 0.45 {
			t.Errorf("BM25Weight = %f; want 0.45", d.SearchWeights.BM25Weight)
		}
		if d.SearchWeights.VectorWeight != 0.55 {
			t.Errorf("VectorWeight = %f; want 0.55", d.SearchWeights.VectorWeight)
		}
		if d.SearchWeights.RRFK != 60.0 {
			t.Errorf("RRFK = %f; want 60.0", d.SearchWeights.RRFK)
		}
	})

	t.Run("decay config defaults", func(t *testing.T) {
		if d.DecayConfig == nil {
			t.Fatal("DecayConfig should not be nil")
		}
		if d.DecayConfig.MinEdgeNodes != 2 {
			t.Errorf("MinEdgeNodes = %d; want 2", d.DecayConfig.MinEdgeNodes)
		}
	})

	t.Run("session config defaults", func(t *testing.T) {
		if d.SessionConfig == nil {
			t.Fatal("SessionConfig should not be nil")
		}
		if d.SessionConfig.DefaultTTLMs != 3600000 {
			t.Errorf("DefaultTTLMs = %d; want 3600000", d.SessionConfig.DefaultTTLMs)
		}
		if d.SessionConfig.Capacity != 7 {
			t.Errorf("Capacity = %d; want 7", d.SessionConfig.Capacity)
		}
	})

	t.Run("llm preprocess defaults", func(t *testing.T) {
		if d.LlmPreprocess == nil {
			t.Fatal("LlmPreprocess should not be nil")
		}
		if d.LlmPreprocess.PreprocessMaxTokens != 512 {
			t.Errorf("PreprocessMaxTokens = %d; want 512", d.LlmPreprocess.PreprocessMaxTokens)
		}
	})

	if d.AdjacencyCacheMaxEntries != 128 {
		t.Errorf("AdjacencyCacheMaxEntries = %d; want 128", d.AdjacencyCacheMaxEntries)
	}
	if d.TokenizerEngine != "auto" {
		t.Errorf("TokenizerEngine = %q; want \"auto\"", d.TokenizerEngine)
	}
}
