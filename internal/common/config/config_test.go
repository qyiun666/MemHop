// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package config

import (
	"errors"
	"math"
	"testing"

	"github.com/qyiun666/MemHop/internal/common/mherrors"
)

// validBase returns a config with all required fields populated so each
// test can focus on the single field it mutates.
func validBase() *MemHopConfig {
	return &MemHopConfig{
		DBPath:     "/tmp/test.meh",
		VectorDim:  768,
		EmbedModel: "test-embed",
		LLM:        LlmConfig{APIURL: "http://localhost", APIKey: "k", Model: "m"},
	}
}

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
			c := validBase()
			c.VectorDim = tt.dim
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
	c := validBase()
	if err := c.Validate(); err != nil {
		t.Fatalf("expected nil when Defaults is nil, got %v", err)
	}
}

func TestValidateRequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*MemHopConfig)
	}{
		{"missing DBPath", func(c *MemHopConfig) { c.DBPath = "" }},
		{"missing EmbedModel", func(c *MemHopConfig) { c.EmbedModel = "" }},
		{"missing LLM APIURL", func(c *MemHopConfig) { c.LLM.APIURL = "" }},
		{"missing LLM APIKey", func(c *MemHopConfig) { c.LLM.APIKey = "" }},
		{"missing LLM Model", func(c *MemHopConfig) { c.LLM.Model = "" }},
		{"negative encoder timeout", func(c *MemHopConfig) { c.EncoderTimeoutSecs = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validBase()
			tt.mutate(c)
			err := c.Validate()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, mherrors.ErrConfig) {
				t.Errorf("error should wrap ErrConfig")
			}
		})
	}
}

func TestValidateNegativeSearchWeights(t *testing.T) {
	tests := []struct {
		name  string
		setup func() *SearchWeights
	}{
		{"negative RRFK", func() *SearchWeights {
			return &SearchWeights{RRFK: -1, ActivationBonus: 0.1, RecentChatBonus: 0.1}
		}},
		{"negative ActivationBonus", func() *SearchWeights {
			return &SearchWeights{RRFK: 60, ActivationBonus: -0.1, RecentChatBonus: 0.1}
		}},
		{"negative RecentChatBonus", func() *SearchWeights {
			return &SearchWeights{RRFK: 60, ActivationBonus: 0.1, RecentChatBonus: -0.1}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validBase()
			c.Defaults = &MemHopDefaults{
				SearchWeights: tt.setup(),
			}
			err := c.Validate()
			if err == nil {
				t.Fatal("expected error for negative search weight")
			}
		})
	}
}

func TestValidateValidSearchWeights(t *testing.T) {
	c := validBase()
	c.Defaults = &MemHopDefaults{
		SearchWeights: &SearchWeights{
			RRFK:            60,
			ActivationBonus: 0.02, RecentChatBonus: 0.01,
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
			c := validBase()
			c.Defaults = &MemHopDefaults{
				DecayConfig: tt.decay,
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
		if d.SearchWeights.RRFK != 60.0 {
			t.Errorf("RRFK = %f; want 60.0", d.SearchWeights.RRFK)
		}
		if d.SearchWeights.ActivationBonus != 0.02 {
			t.Errorf("ActivationBonus = %f; want 0.02", d.SearchWeights.ActivationBonus)
		}
		if d.SearchWeights.RecentChatBonus != 0.01 {
			t.Errorf("RecentChatBonus = %f; want 0.01", d.SearchWeights.RecentChatBonus)
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

	if d.AdjacencyCacheMaxEntries != 128 {
		t.Errorf("AdjacencyCacheMaxEntries = %d; want 128", d.AdjacencyCacheMaxEntries)
	}
	if d.TokenizerEngine != "auto" {
		t.Errorf("TokenizerEngine = %q; want \"auto\"", d.TokenizerEngine)
	}
}
