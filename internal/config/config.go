// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package config holds the types that describe how a database is set up: the LLM
// endpoint and the tuning knobs. Validation lives on the type that owns the rule, so
// no knob is declared twice.

package config

import (
	"fmt"
	"math"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
)

// MemHopConfig is one database's assembled configuration: where the file lives, which
// endpoint answers, which tuning applies. The LLM endpoint is the only external
// service the engine contacts, so there are no retrieval or embedding settings.
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

// MaxTimeoutSecs is the longest request timeout the client can be given: past it the
// seconds no longer fit a time.Duration once scaled to nanoseconds. A wrapped duration is
// negative, and a negative timeout is the absence of one — the exact outcome the knob's
// low end guards against, since every LLM call runs inside its domain's lock.
const MaxTimeoutSecs = int64(math.MaxInt64 / int64(time.Second))

// Validate reports whether one LLM endpoint is fully specified: there is no fallback
// for a call it cannot make, so a half-filled one is refused at the boundary.
func (c LlmConfig) Validate() error {
	if c.APIURL == "" || c.APIKey == "" || c.Model == "" {
		return common.NewError(common.ErrConfig, "LLM.APIURL, LLM.APIKey and LLM.Model are required")
	}
	// Compared in int64 so a 32-bit build — where no int can reach the ceiling — carries
	// the same rule instead of a constant that would not fit.
	if int64(c.TimeoutSecs) > MaxTimeoutSecs {
		return common.NewError(common.ErrConfig, fmt.Sprintf(
			"LLM.TimeoutSecs %d is past the longest timeout the client can hold (%d s): a longer one wraps the duration around, and the HTTP client reads a non-positive timeout as no timeout at all, which leaves a hung endpoint blocking this agent domain forever",
			c.TimeoutSecs, MaxTimeoutSecs))
	}
	return nil
}
