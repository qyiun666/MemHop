// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package testsupport provides shared helpers for integration tests that
// run against real Ollama (encoder) and DeepSeek (LLM) services.
package testsupport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	memhop "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/sub"
)

// LLM config environment variables. Priority: env vars > key_config.json file.
const (
	EnvLLMKey   = "MEMHOP_TEST_LLM_KEY"
	EnvLLMURL   = "MEMHOP_TEST_LLM_URL"
	EnvLLMModel = "MEMHOP_TEST_LLM_MODEL"
)

// Defaults used when only MEMHOP_TEST_LLM_KEY is set.
const (
	defaultLLMURL   = "https://api.deepseek.com/v1/chat/completions"
	defaultLLMModel = "deepseek-chat"
)

// errNoLLMConfig is returned when neither env vars nor key_config.json
// provide an LLM API key.
var errNoLLMConfig = errors.New("testsupport: no LLM config: set " + EnvLLMKey +
	" or create test/testsupport/key_config.json")

func keyConfigPath() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "key_config.json")
}

// LoadLLMConfig fills cfg.LLM from env vars first, then key_config.json.
// Exported for tests that build their own LLM client (e.g. the quality judge).
func LoadLLMConfig(cfg *sub.MemHopConfig) error { return loadLLMConfig(cfg) }

// loadLLMConfig fills cfg.LLM from env vars first, then key_config.json.
func loadLLMConfig(cfg *sub.MemHopConfig) error {
	if key := os.Getenv(EnvLLMKey); key != "" {
		cfg.LLM.APIKey = key
		cfg.LLM.APIURL = os.Getenv(EnvLLMURL)
		if cfg.LLM.APIURL == "" {
			cfg.LLM.APIURL = defaultLLMURL
		}
		cfg.LLM.Model = os.Getenv(EnvLLMModel)
		if cfg.LLM.Model == "" {
			cfg.LLM.Model = defaultLLMModel
		}
		cfg.LLM.TimeoutSecs = 120
		return nil
	}

	f, err := os.Open(keyConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return errNoLLMConfig
		}
		return fmt.Errorf("testsupport: read key_config.json: %w", err)
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&cfg.LLM); err != nil {
		return fmt.Errorf("testsupport: parse key_config.json: %w", err)
	}
	if cfg.LLM.APIKey == "" {
		return errNoLLMConfig
	}
	if cfg.LLM.TimeoutSecs <= 0 {
		cfg.LLM.TimeoutSecs = 120
	}
	return nil
}

// OpenMemHop opens a MemHop database backed by real services
// (Ollama encoder + DeepSeek LLM). The DB file lives in t.TempDir().
// It calls t.Skip when LLM config is missing or Ollama is unavailable,
// and t.Fatal on any other error. The caller must call Close() when done.
func OpenMemHop(t *testing.T) *memhop.DB {
	t.Helper()
	return open(t)
}

// OpenMemHopB is the *testing.B variant of OpenMemHop.
func OpenMemHopB(b *testing.B) *memhop.DB {
	b.Helper()
	return open(b)
}

// open is the shared implementation for testing.T and testing.B.
func open(tb testing.TB) *memhop.DB {
	cfg := &sub.MemHopConfig{
		DBPath:      filepath.Join(tb.TempDir(), "test.meh"),
		VectorDim:   1024,
		EncoderAddr: "http://127.0.0.1:11434",
		EmbedModel:  "qllama/bge-m3:q4_k_m",
		Defaults:    *sub.DefaultMemHopDefaults,
	}
	if err := loadLLMConfig(cfg); err != nil {
		tb.Skipf("跳过真实依赖测试: %v", err)
	}

	db, err := memhop.Open(cfg)
	if err != nil {
		tb.Fatalf("memhop.Open: %v", err)
	}
	return db
}
