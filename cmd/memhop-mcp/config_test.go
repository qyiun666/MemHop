// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"testing"
)

func TestLoadConfigRequired(t *testing.T) {
	t.Setenv("MEMHOP_LLM_API_URL", "http://llm.local/v1")
	t.Setenv("MEMHOP_LLM_API_KEY", "test-key")
	t.Setenv("MEMHOP_LLM_MODEL", "gpt-test")
	cfg, err := loadConfig([]string{
		"--db-dir", "/tmp/homes",
		"--vector-dim", "384",
		"--encoder-addr", "http://127.0.0.1:11434",
		"--embed-model", "bge-m3",
	})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.DBDir != "/tmp/homes" || cfg.Base.VectorDim != 384 {
		t.Errorf("flag values not applied: %+v", cfg)
	}
	if cfg.Base.EncoderAddr != "http://127.0.0.1:11434" || cfg.Base.EmbedModel != "bge-m3" {
		t.Errorf("encoder config mismatch: %+v", cfg.Base)
	}
	if cfg.Base.LLM.APIKey != "test-key" || cfg.Base.LLM.Model != "gpt-test" {
		t.Errorf("llm env not applied: %+v", cfg.Base.LLM)
	}
}

func TestLoadConfigFlagWinsOverEnv(t *testing.T) {
	t.Setenv("MEMHOP_LLM_API_URL", "http://env.local/v1")
	t.Setenv("MEMHOP_LLM_API_KEY", "env-key")
	t.Setenv("MEMHOP_LLM_MODEL", "env-model")
	cfg, err := loadConfig([]string{
		"--db-dir", "/tmp/homes",
		"--embed-model", "bge-m3",
		"--llm-model", "flag-model",
	})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Base.LLM.Model != "flag-model" {
		t.Errorf("flag should win over env, got %q", cfg.Base.LLM.Model)
	}
	if cfg.Base.LLM.APIURL != "http://env.local/v1" {
		t.Errorf("env url not applied: %q", cfg.Base.LLM.APIURL)
	}
}

func TestLoadConfigValidation(t *testing.T) {
	// Missing db-dir must fail.
	if _, err := loadConfig([]string{"--embed-model", "bge-m3"}); err == nil {
		t.Fatal("expected error for missing --db-dir")
	}
	// Missing LLM env must fail.
	t.Setenv("MEMHOP_LLM_API_URL", "")
	t.Setenv("MEMHOP_LLM_API_KEY", "")
	t.Setenv("MEMHOP_LLM_MODEL", "")
	if _, err := loadConfig([]string{"--db-dir", "/tmp/homes", "--embed-model", "bge-m3"}); err == nil {
		t.Fatal("expected validation error for missing LLM config")
	}
	// Missing embed-model must fail.
	t.Setenv("MEMHOP_LLM_API_URL", "http://llm.local/v1")
	t.Setenv("MEMHOP_LLM_API_KEY", "k")
	t.Setenv("MEMHOP_LLM_MODEL", "m")
	if _, err := loadConfig([]string{"--db-dir", "/tmp/homes"}); err == nil {
		t.Fatal("expected validation error for missing --embed-model")
	}
	// Bad integer env must fail.
	t.Setenv("MEMHOP_LLM_TIMEOUT_SECS", "abc")
	if _, err := loadConfig([]string{"--db-dir", "/tmp/homes", "--embed-model", "bge-m3"}); err == nil {
		t.Fatal("expected error for non-integer MEMHOP_LLM_TIMEOUT_SECS")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("MEMHOP_LLM_API_URL", "http://llm.local/v1")
	t.Setenv("MEMHOP_LLM_API_KEY", "k")
	t.Setenv("MEMHOP_LLM_MODEL", "m")
	cfg, err := loadConfig([]string{"--db-dir", "/tmp/homes", "--embed-model", "bge-m3"})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Listen != "127.0.0.1:3939" {
		t.Errorf("default listen mismatch: %q", cfg.Listen)
	}
	if cfg.Base.EncoderTimeoutSecs != 20 || cfg.Base.LLM.TimeoutSecs != 30 || cfg.Base.LLM.MaxOutputTokens != 8192 {
		t.Errorf("defaults mismatch: %+v", cfg.Base)
	}
}

func TestLoadConfigTenants(t *testing.T) {
	t.Setenv("MEMHOP_LLM_API_URL", "http://llm.local/v1")
	t.Setenv("MEMHOP_LLM_API_KEY", "k")
	t.Setenv("MEMHOP_LLM_MODEL", "m")

	cfg, err := loadConfig([]string{
		"--db-dir", "/tmp/homes", "--embed-model", "bge-m3",
		"--tenants", "alice, bob ,,carol-1",
	})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(cfg.Tenants) != 3 || cfg.Tenants[0] != "alice" || cfg.Tenants[1] != "bob" || cfg.Tenants[2] != "carol-1" {
		t.Errorf("tenants parse mismatch: %v", cfg.Tenants)
	}

	// Invalid tenant ids are rejected at load time.
	if _, err := loadConfig([]string{
		"--db-dir", "/tmp/homes", "--embed-model", "bge-m3", "--tenants", "alice,../evil",
	}); err == nil {
		t.Fatal("expected error for invalid tenant id in --tenants")
	}
}
