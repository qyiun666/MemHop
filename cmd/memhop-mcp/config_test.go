// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"strings"
	"testing"
)

// setLLMEnv fills the required MEMHOP_LLM_* environment variables.
func setLLMEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MEMHOP_LLM_API_URL", "http://llm:9999/v1")
	t.Setenv("MEMHOP_LLM_API_KEY", "test-key")
	t.Setenv("MEMHOP_LLM_MODEL", "test-model")
}

func TestLoadConfigRequired(t *testing.T) {
	setLLMEnv(t)
	cfg, err := loadConfig([]string{
		"--db-dir", "/tmp/meh",
		"--embed-model", "bge-m3",
	})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Listen != "127.0.0.1:3939" {
		t.Errorf("default listen = %q, want 127.0.0.1:3939", cfg.Listen)
	}
	if cfg.Transport != "sse" {
		t.Errorf("default transport = %q, want sse", cfg.Transport)
	}
	if cfg.Base.VectorDim != 1024 {
		t.Errorf("default vector-dim = %d, want 1024", cfg.Base.VectorDim)
	}
	if cfg.Base.EmbedModel != "bge-m3" {
		t.Errorf("embed-model = %q, want bge-m3", cfg.Base.EmbedModel)
	}
	if cfg.Base.LLM.APIURL != "http://llm:9999/v1" || cfg.Base.LLM.APIKey != "test-key" || cfg.Base.LLM.Model != "test-model" {
		t.Errorf("LLM config from env mismatch: %+v", cfg.Base.LLM)
	}
	if len(cfg.Tenants) != 0 {
		t.Errorf("tenants = %v, want empty", cfg.Tenants)
	}
}

func TestLoadConfigFlagsWinOverEnv(t *testing.T) {
	setLLMEnv(t)
	cfg, err := loadConfig([]string{
		"--db-dir", "/tmp/meh",
		"--embed-model", "bge-m3",
		"--listen", "0.0.0.0:9000",
		"--transport", "streamable-http",
		"--tenants", "alice,bob",
		"--vector-dim", "512",
		"--llm-model", "flag-model",
	})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Listen != "0.0.0.0:9000" {
		t.Errorf("listen = %q, want 0.0.0.0:9000", cfg.Listen)
	}
	if cfg.Transport != "streamable-http" {
		t.Errorf("transport = %q, want streamable-http", cfg.Transport)
	}
	if len(cfg.Tenants) != 2 || cfg.Tenants[0] != "alice" || cfg.Tenants[1] != "bob" {
		t.Errorf("tenants = %v, want [alice bob]", cfg.Tenants)
	}
	if cfg.Base.VectorDim != 512 {
		t.Errorf("vector-dim = %d, want 512", cfg.Base.VectorDim)
	}
	if cfg.Base.LLM.Model != "flag-model" {
		t.Errorf("llm-model = %q, want flag-model (flag wins)", cfg.Base.LLM.Model)
	}
}

func TestLoadConfigEnvInt(t *testing.T) {
	setLLMEnv(t)
	t.Setenv("MEMHOP_LLM_TIMEOUT_SECS", "60")
	t.Setenv("MEMHOP_LLM_MAX_OUTPUT_TOKENS", "4096")
	cfg, err := loadConfig([]string{"--db-dir", "/tmp/meh", "--embed-model", "bge-m3"})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Base.LLM.TimeoutSecs != 60 || cfg.Base.LLM.MaxOutputTokens != 4096 {
		t.Errorf("LLM env ints = %d/%d, want 60/4096", cfg.Base.LLM.TimeoutSecs, cfg.Base.LLM.MaxOutputTokens)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	setLLMEnv(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing db-dir", []string{"--embed-model", "bge-m3"}, "--db-dir is required"},
		{"missing embed-model", []string{"--db-dir", "/tmp/meh"}, "--embed-model is required"},
		{"bad transport", []string{"--db-dir", "/tmp/meh", "--embed-model", "bge-m3", "--transport", "stdio"}, "--transport must be sse or streamable-http"},
		{"bad vector-dim", []string{"--db-dir", "/tmp/meh", "--embed-model", "bge-m3", "--vector-dim", "0"}, "vector-dim must be in range"},
		{"bad tenant", []string{"--db-dir", "/tmp/meh", "--embed-model", "bge-m3", "--tenants", "alice/../root"}, "invalid tenant id"},
		{"positional args", []string{"--db-dir", "/tmp/meh", "--embed-model", "bge-m3", "extra"}, "unexpected positional arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadConfig(tc.args)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want containing %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLoadConfigMissingLLMEnv(t *testing.T) {
	t.Setenv("MEMHOP_LLM_API_URL", "")
	t.Setenv("MEMHOP_LLM_API_KEY", "")
	t.Setenv("MEMHOP_LLM_MODEL", "")
	_, err := loadConfig([]string{"--db-dir", "/tmp/meh", "--embed-model", "bge-m3"})
	if err == nil || !strings.Contains(err.Error(), "MEMHOP_LLM_API_URL") {
		t.Errorf("expected missing LLM env error, got %v", err)
	}
}

func TestLoadConfigEnvIntInvalid(t *testing.T) {
	setLLMEnv(t)
	t.Setenv("MEMHOP_LLM_TIMEOUT_SECS", "not-a-number")
	_, err := loadConfig([]string{"--db-dir", "/tmp/meh", "--embed-model", "bge-m3"})
	if err == nil || !strings.Contains(err.Error(), "MEMHOP_LLM_TIMEOUT_SECS") {
		t.Errorf("expected env int error, got %v", err)
	}
}
