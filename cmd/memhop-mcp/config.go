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
	DBDir  string // root directory holding one .meh file per tenant
	// Tenants is the optional tenant whitelist; empty allows any valid
	// tenant id to create its database on first access.
	Tenants []string
	// Transport selects the multi-tenant HTTP transport: "sse" (default,
	// 2024-11-05 spec) or "streamable-http" (2025-03-26 spec, supported by
	// dsh-mcp-client and other modern MCP clients).
	Transport string
	// Base is the shared engine configuration. DBPath is left empty here and
	// filled per tenant as <DBDir>/<tenant-id>.meh by the tenant registry.
	Base memhop.MemHopConfig
}

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

// loadConfig parses flags and environment into a serverConfig.
func loadConfig(args []string) (*serverConfig, error) {
	fs := flag.NewFlagSet("memhop-mcp", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:3939", "HTTP listen address")
	dbDir := fs.String("db-dir", "", "directory holding one .meh database per tenant (required)")
	tenants := fs.String("tenants", "", "optional comma-separated tenant whitelist")
	transport := fs.String("transport", "sse", "multi-tenant HTTP transport: sse or streamable-http")
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
	if *dbDir == "" {
		return nil, fmt.Errorf("--db-dir is required")
	}
	if *transport != "sse" && *transport != "streamable-http" {
		return nil, fmt.Errorf("--transport must be sse or streamable-http, got %q", *transport)
	}

	llmTimeout, err := envInt("MEMHOP_LLM_TIMEOUT_SECS", 30)
	if err != nil {
		return nil, err
	}
	llmMaxTokens, err := envInt("MEMHOP_LLM_MAX_OUTPUT_TOKENS", 8192)
	if err != nil {
		return nil, err
	}

	base := memhop.MemHopConfig{
		DBPath:             "", // filled per tenant by the registry
		VectorDim:          *vectorDim,
		EncoderAddr:        *encoderAddr,
		EmbedModel:         *embedModel,
		EncoderTimeoutSecs: *encoderTimeoutSecs,
	}
	base.LLM.APIURL = envOr("MEMHOP_LLM_API_URL", "")
	base.LLM.APIKey = os.Getenv("MEMHOP_LLM_API_KEY")
	base.LLM.Model = firstNonEmpty(*llmModel, os.Getenv("MEMHOP_LLM_MODEL"))
	base.LLM.TimeoutSecs = llmTimeout
	base.LLM.MaxOutputTokens = llmMaxTokens

	// Field-level checks mirroring MemHopConfig.Validate minus DBPath
	// (filled per tenant by the registry, which runs the full Validate).
	if base.VectorDim <= 0 || base.VectorDim > 65535 {
		return nil, fmt.Errorf("vector-dim must be in range (0, 65535]")
	}
	if base.EmbedModel == "" {
		return nil, fmt.Errorf("--embed-model is required")
	}
	if base.LLM.APIURL == "" || base.LLM.APIKey == "" || base.LLM.Model == "" {
		return nil, fmt.Errorf("MEMHOP_LLM_API_URL, MEMHOP_LLM_API_KEY and MEMHOP_LLM_MODEL are required")
	}

	allowed, err := splitTenants(*tenants)
	if err != nil {
		return nil, err
	}
	return &serverConfig{
		Listen:    *listen,
		DBDir:     *dbDir,
		Tenants:   allowed,
		Transport: *transport,
		Base:      base,
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
