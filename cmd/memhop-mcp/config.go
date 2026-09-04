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
	// CapabilityDir anchors the paths memhop_capability_import may read. It
	// defaults to DBDir: an LLM names the file it wants imported, so the
	// directory has to be one the operator chose rather than one the model picks.
	CapabilityDir string
	// Tenants is the optional tenant whitelist; empty allows any valid
	// tenant id to open its agent domain on first access.
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
	listen    string
	dbDir     string
	capDir    string
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
	fs.StringVar(&v.capDir, "capability-dir", "", "directory memhop_capability_import paths are anchored to (default: --db-dir)")
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
	// DBPath is filled by the registry with the shared <db-dir>/memhop.meh.
	base = memhop.MemHopConfig{}
	base.LLM.APIURL = envOr("MEMHOP_LLM_API_URL", "")
	base.LLM.APIKey = os.Getenv("MEMHOP_LLM_API_KEY")
	base.LLM.Model = firstNonEmpty(v.llmModel, os.Getenv("MEMHOP_LLM_MODEL"))
	base.LLM.TimeoutSecs = llmTimeout
	base.LLM.MaxOutputTokens = llmMaxTokens
	// Field-level checks mirroring MemHopConfig.Validate minus DBPath
	// (the registry fills the shared database path and runs the full Validate).
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
		Listen:        v.listen,
		DBDir:         v.dbDir,
		CapabilityDir: firstNonEmpty(v.capDir, os.Getenv("MEMHOP_CAPABILITY_DIR"), v.dbDir),
		Tenants:       allowed,
		Transport:     v.transport,
		Base:          base,
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
