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
	// tenant id to create its database on first access.
	Tenants []string
	// Transport selects the multi-tenant HTTP transport: "sse" (default,
	// 2024-11-05 spec) or "streamable-http" (2025-03-26 spec, supported by
	// dsh-mcp-client and other modern MCP clients).
	Transport string
	// Base is the shared engine configuration. DBPath is left empty here and
	// filled once by the registry with <DBDir>/memhop.meh.
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

// flagValues holds the parsed command-line flags of the server.
type flagValues struct {
	listen             string
	dbDir              string
	tenants            string
	transport          string
	vectorDim          int
	encoderAddr        string
	embedModel         string
	encoderTimeoutSecs int
	llmModel           string
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
	fs.IntVar(&v.vectorDim, "vector-dim", 1024, "embedding vector dimension")
	fs.StringVar(&v.encoderAddr, "encoder-addr", "", "embedding encoder HTTP address (e.g. http://127.0.0.1:11434)")
	fs.StringVar(&v.embedModel, "embed-model", "", "embedding model name on the encoder (required)")
	fs.IntVar(&v.encoderTimeoutSecs, "encoder-timeout-secs", 20, "encoder request timeout in seconds")
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
	if v.transport != "sse" && v.transport != "streamable-http" {
		return nil, fmt.Errorf("--transport must be sse or streamable-http, got %q", v.transport)
	}
	return v, nil
}

// buildBaseConfig assembles the shared engine config from flags plus the
// MEMHOP_* environment (LLM credentials come from the environment only).
func buildBaseConfig(v *flagValues) (memhop.MemHopConfig, error) {
	var base memhop.MemHopConfig
	llmTimeout, err := envInt("MEMHOP_LLM_TIMEOUT_SECS", 30)
	if err != nil {
		return base, err
	}
	llmMaxTokens, err := envInt("MEMHOP_LLM_MAX_OUTPUT_TOKENS", 8192)
	if err != nil {
		return base, err
	}
	base = memhop.MemHopConfig{
		DBPath:             "", // filled per tenant by the registry
		VectorDim:          v.vectorDim,
		EncoderAddr:        v.encoderAddr,
		EmbedModel:         v.embedModel,
		EncoderTimeoutSecs: v.encoderTimeoutSecs,
	}
	base.LLM.APIURL = envOr("MEMHOP_LLM_API_URL", "")
	base.LLM.APIKey = os.Getenv("MEMHOP_LLM_API_KEY")
	base.LLM.Model = firstNonEmpty(v.llmModel, os.Getenv("MEMHOP_LLM_MODEL"))
	base.LLM.TimeoutSecs = llmTimeout
	base.LLM.MaxOutputTokens = llmMaxTokens
	// Field-level checks mirroring MemHopConfig.Validate minus DBPath
	// (filled per tenant by the registry, which runs the full Validate).
	if base.VectorDim <= 0 || base.VectorDim > 65535 {
		return base, fmt.Errorf("vector-dim must be in range (0, 65535]")
	}
	if base.EmbedModel == "" {
		return base, fmt.Errorf("--embed-model is required")
	}
	if base.LLM.APIURL == "" || base.LLM.APIKey == "" || base.LLM.Model == "" {
		return base, fmt.Errorf("MEMHOP_LLM_API_URL, MEMHOP_LLM_API_KEY and MEMHOP_LLM_MODEL are required")
	}
	return base, nil
}

// loadConfig parses flags and environment into a serverConfig.
func loadConfig(args []string) (*serverConfig, error) {
	v, err := parseFlags(args)
	if err != nil {
		return nil, err
	}
	base, err := buildBaseConfig(v)
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
