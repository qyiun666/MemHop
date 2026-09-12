// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package config holds the types that describe how a database is set up: the LLM
// endpoint and the tuning defaults a host hands to Open, and the bundle the
// composition root assembles out of them. Validation lives on the type that owns
// the rule, so no knob is declared twice.

package config

import (
	"github.com/qyiun666/MemHop/internal/common"
)

// MemHopConfig is one database's assembled configuration: where the file lives,
// which endpoint answers, which tuning applies. The composition root builds it out
// of Open's arguments; the LLM endpoint is the only external service the engine
// contacts, so there are no retrieval or embedding settings to carry.
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

// Validate reports whether one LLM endpoint is fully specified. The engine's
// only external service is this endpoint and there is no fallback for a call it
// cannot make, so a half-filled one is refused at the boundary rather than
// surfacing as a transport error mid-turn.
func (c LlmConfig) Validate() error {
	if c.APIURL == "" || c.APIKey == "" || c.Model == "" {
		return common.NewError(common.ErrConfig, "LLM.APIURL, LLM.APIKey and LLM.Model are required")
	}
	return nil
}
