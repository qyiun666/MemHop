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
	"strings"

	memhop "github.com/qyiun666/MemHop/api"
)

// serverConfig is the resolved memhop-mcp configuration.
type serverConfig struct {
	Listen string // HTTP listen address
	DBDir  string // root directory holding the shared memhop.meh multi-agent database
	// Tenants is the optional tenant whitelist; empty allows any valid
	// tenant id to open its agent domain on first access.
	Tenants []string
	// Transport selects the multi-tenant HTTP transport: "sse" (default,
	// 2024-11-05 spec) or "streamable-http" (2025-03-26 spec, supported by
	// dsh-mcp-client and other modern MCP clients).
	Transport string
	// LLM is the endpoint every tenant domain runs on. DBPath is not here: the
	// registry resolves it to <DBDir>/memhop.meh.
	LLM memhop.LlmConfig
	// Defaults is the engine's tuning knobs. It stays at its zero value, which
	// is what this server has always run with: no automatic consolidation
	// trigger (SceneDreamTopicThreshold <= 0 disables it), a compress floor of
	// zero, and no idle-domain reclaim. Whether it should instead run on
	// memhop.DefaultMemHopDefaults is a behaviour change, not a facade one, so
	// it is left as it is and called out here rather than changed in passing.
	Defaults memhop.MemHopDefaults
}

// splitTenants parses a comma-separated tenant whitelist, dropping empty
// entries. Invalid ids are rejected at load time, not on first access.
func splitTenants(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		id := strings.TrimSpace(part)
		if id == "" {
			continue
		}
		if !tenantIDRe.MatchString(id) {
			return nil, fmt.Errorf("invalid tenant id %q in --tenants", id)
		}
		out = append(out, id)
	}
	return out, nil
}

// flagValues holds the parsed command-line flags of the server.
type flagValues struct {
	listen    string
	dbDir     string
	tenants   string
	transport string
	llmModel  string
}

// parseFlags registers and parses the memhop-mcp command line, rejecting
// unknown positional args and enforcing flag-level invariants.
func parseFlags(args []string) (*flagValues, error) {
	fs := flag.NewFlagSet("memhop-mcp", flag.ContinueOnError)
	v := &flagValues{}
	fs.StringVar(&v.listen, "listen", "127.0.0.1:3939", "HTTP listen address")
	fs.StringVar(&v.dbDir, "db-dir", "", "directory holding the shared multi-agent memhop.meh database (required)")
	fs.StringVar(&v.tenants, "tenants", "", "optional comma-separated tenant whitelist")
	fs.StringVar(&v.transport, "transport", "sse", "multi-tenant HTTP transport: sse or streamable-http")
	fs.StringVar(&v.llmModel, "llm-model", "", "LLM model name (overrides MEMHOP_LLM_MODEL)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	if v.dbDir == "" {
		return nil, fmt.Errorf("--db-dir is required")
	}
	return v, nil
}

// buildBaseLLM assembles the LLM endpoint from flags plus the MEMHOP_*
// environment (credentials come from the environment only). The endpoint is
// validated here rather than left to Open, so a half-specified one stops the
// process at startup instead of the first request.
func buildBaseLLM(v *flagValues) (memhop.LlmConfig, error) {
	llmTimeout, err := envInt("MEMHOP_LLM_TIMEOUT_SECS", 30)
	if err != nil {
		return memhop.LlmConfig{}, err
	}
	llmMaxTokens, err := envInt("MEMHOP_LLM_MAX_OUTPUT_TOKENS", 8192)
	if err != nil {
		return memhop.LlmConfig{}, err
	}
	model := v.llmModel
	if model == "" {
		model = os.Getenv("MEMHOP_LLM_MODEL")
	}
	llm := memhop.LlmConfig{
		APIURL:          os.Getenv("MEMHOP_LLM_API_URL"),
		APIKey:          os.Getenv("MEMHOP_LLM_API_KEY"),
		Model:           model,
		TimeoutSecs:     llmTimeout,
		MaxOutputTokens: llmMaxTokens,
	}
	if err := llm.Validate(); err != nil {
		// The rule is the library's; the actionable names are this binary's, so a
		// misconfigured server says which environment variables to set.
		return memhop.LlmConfig{}, fmt.Errorf(
			"LLM endpoint is not configured: set MEMHOP_LLM_API_URL, MEMHOP_LLM_API_KEY and MEMHOP_LLM_MODEL: %w", err)
	}
	return llm, nil
}

// loadConfig parses flags and environment into a serverConfig.
func loadConfig(args []string) (*serverConfig, error) {
	v, err := parseFlags(args)
	if err != nil {
		return nil, err
	}
	base, err := buildBaseLLM(v)
	if err != nil {
		return nil, err
	}
	allowed, err := splitTenants(v.tenants)
	if err != nil {
		return nil, err
	}
	return &serverConfig{
		Listen:    v.listen,
		DBDir:     v.dbDir,
		Tenants:   allowed,
		Transport: v.transport,
		LLM:       base,
	}, nil
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
