// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package config holds the host-facing configuration types of the memory
// engine. The composition root (internal) validates and consumes them; the
// domain and the small-method packages read the business knobs through the
// same types, so no package re-declares a knob.

package config

import (
	"github.com/qyiun666/MemHop/internal/common"
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
