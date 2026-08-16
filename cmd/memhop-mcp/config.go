// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Config loading for the memhop-mcp server: command-line flags plus
// MEMHOP_* environment variables. LLM credentials are read from the
// environment only (never flags), keeping them out of process listings and
// MCP client configurations.

package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	memhop "github.com/qyiun666/MemHop"
)

// envOr returns the environment value when set, otherwise the fallback.
func envOr(envKey, fallback string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return fallback
}

// firstNonEmpty returns the first non-empty value (flags win over env).
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// loadConfig parses flags and environment into a MemHopConfig.
func loadConfig(args []string) (*memhop.MemHopConfig, error) {
	fs := flag.NewFlagSet("memhop-mcp", flag.ContinueOnError)
	dbPath := fs.String("db", "", "path to the .meh database file (required)")
	vectorDim := fs.Int("vector-dim", 1024, "embedding vector dimension")
	encoderAddr := fs.String("encoder-addr", "", "embedding encoder HTTP address (e.g. http://127.0.0.1:11434)")
	embedModel := fs.String("embed-model", "", "embedding model name on the encoder (required)")
	encoderTimeoutSecs := fs.Int("encoder-timeout-secs", 20, "encoder request timeout in seconds")
	llmModel := fs.String("llm-model", "", "LLM model name (overrides MEMHOP_LLM_MODEL)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}

	llmTimeout, err := envInt("MEMHOP_LLM_TIMEOUT_SECS", 30)
	if err != nil {
		return nil, err
	}
	llmMaxTokens, err := envInt("MEMHOP_LLM_MAX_OUTPUT_TOKENS", 2048)
	if err != nil {
		return nil, err
	}

	cfg := &memhop.MemHopConfig{
		DBPath:             *dbPath,
		VectorDim:          *vectorDim,
		EncoderAddr:        *encoderAddr,
		EmbedModel:         *embedModel,
		EncoderTimeoutSecs: *encoderTimeoutSecs,
		LLM: memhop.LlmConfig{
			APIURL:          envOr("MEMHOP_LLM_API_URL", ""),
			APIKey:          os.Getenv("MEMHOP_LLM_API_KEY"),
			Model:           firstNonEmpty(*llmModel, os.Getenv("MEMHOP_LLM_MODEL")),
			TimeoutSecs:     llmTimeout,
			MaxOutputTokens: llmMaxTokens,
		},
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func envInt(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", key, v)
	}
	return n, nil
}
