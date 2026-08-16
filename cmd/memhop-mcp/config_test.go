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
		"--db", "/tmp/test.meh",
		"--vector-dim", "384",
		"--encoder-addr", "http://127.0.0.1:11434",
		"--embed-model", "bge-m3",
	})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.DBPath != "/tmp/test.meh" || cfg.VectorDim != 384 {
		t.Errorf("flag values not applied: %+v", cfg)
	}
	if cfg.EncoderAddr != "http://127.0.0.1:11434" || cfg.EmbedModel != "bge-m3" {
		t.Errorf("encoder config mismatch: %+v", cfg)
	}
	if cfg.LLM.APIKey != "test-key" || cfg.LLM.Model != "gpt-test" {
		t.Errorf("llm env not applied: %+v", cfg.LLM)
	}
}

func TestLoadConfigFlagWinsOverEnv(t *testing.T) {
	t.Setenv("MEMHOP_LLM_API_URL", "http://env.local/v1")
	t.Setenv("MEMHOP_LLM_API_KEY", "env-key")
	t.Setenv("MEMHOP_LLM_MODEL", "env-model")
	cfg, err := loadConfig([]string{
		"--db", "/tmp/test.meh",
		"--embed-model", "bge-m3",
		"--llm-model", "flag-model",
	})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.LLM.Model != "flag-model" {
		t.Errorf("flag should win over env, got %q", cfg.LLM.Model)
	}
	if cfg.LLM.APIURL != "http://env.local/v1" {
		t.Errorf("env url not applied: %q", cfg.LLM.APIURL)
	}
}

func TestLoadConfigValidation(t *testing.T) {
	// Missing DBPath must fail via cfg.Validate.
	_, err := loadConfig([]string{"--embed-model", "bge-m3"})
	if err == nil {
		t.Fatal("expected validation error for missing db path")
	}
	// Missing LLM env must fail.
	t.Setenv("MEMHOP_LLM_API_URL", "")
	t.Setenv("MEMHOP_LLM_API_KEY", "")
	t.Setenv("MEMHOP_LLM_MODEL", "")
	_, err = loadConfig([]string{"--db", "/tmp/x.meh", "--embed-model", "bge-m3"})
	if err == nil {
		t.Fatal("expected validation error for missing LLM config")
	}
	// Bad integer env must fail.
	t.Setenv("MEMHOP_LLM_API_URL", "http://llm.local/v1")
	t.Setenv("MEMHOP_LLM_API_KEY", "k")
	t.Setenv("MEMHOP_LLM_MODEL", "m")
	t.Setenv("MEMHOP_LLM_TIMEOUT_SECS", "abc")
	if _, err = loadConfig([]string{"--db", "/tmp/x.meh", "--embed-model", "bge-m3"}); err == nil {
		t.Fatal("expected error for non-integer MEMHOP_LLM_TIMEOUT_SECS")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("MEMHOP_LLM_API_URL", "http://llm.local/v1")
	t.Setenv("MEMHOP_LLM_API_KEY", "k")
	t.Setenv("MEMHOP_LLM_MODEL", "m")
	cfg, err := loadConfig([]string{"--db", "/tmp/x.meh", "--embed-model", "bge-m3"})
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.EncoderTimeoutSecs != 20 || cfg.LLM.TimeoutSecs != 30 || cfg.LLM.MaxOutputTokens != 2048 {
		t.Errorf("defaults mismatch: %+v", cfg)
	}
}
