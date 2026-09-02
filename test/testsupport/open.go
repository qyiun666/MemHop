// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package testsupport provides shared helpers for integration tests that run
// against a real LLM service (DeepSeek by default). Embeddings are no longer a
// dependency of the engine.
package testsupport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
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
func LoadLLMConfig(cfg *internal.MemHopConfig) error { return loadLLMConfig(cfg) }

// loadLLMConfig fills cfg.LLM from env vars first, then key_config.json.
func loadLLMConfig(cfg *internal.MemHopConfig) error {
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

// Handle is the test handle of the multi-agent-only API: an agent-domain
// session plus the file-level lifecycle methods of the underlying
// MultiAgentDB (Close / Checkpoint / IsClosed).
type Handle struct {
	*memhop.Session
	m *memhop.MultiAgentDB
}

func (h *Handle) Checkpoint() error { return h.m.Checkpoint() }
func (h *Handle) Close() error      { return h.m.Close() }
func (h *Handle) IsClosed() bool    { return h.m.IsClosed() }

// OpenMemHop opens a DB backed by a real LLM service in t.TempDir(); skips
// when the LLM config is missing, fatals otherwise. The caller must Close() it.
func OpenMemHop(t *testing.T) *Handle {
	t.Helper()
	return open(t)
}

// OpenMemHopB is the *testing.B variant of OpenMemHop.
func OpenMemHopB(b *testing.B) *Handle {
	b.Helper()
	return open(b)
}

// OpenTurn enters the memory loop the way a host does: an empty sceneID asks
// for a fresh session (scene), a non-empty one continues it. It returns the
// scene id and the topic id the engine just opened for the next turn — the id
// Update settles that turn into — so a test calls it once per turn.
func (h *Handle) OpenTurn(sceneID string) (string, string, error) {
	res, err := h.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		return "", "", err
	}
	return res.Scene.SceneID, res.NewTopicID, nil
}

// open is the shared implementation for testing.T and testing.B.
func open(tb testing.TB) *Handle {
	cfg := &internal.MemHopConfig{
		DBPath:   filepath.Join(tb.TempDir(), "test.meh"),
		Defaults: *internal.DefaultMemHopDefaults,
	}
	if err := loadLLMConfig(cfg); err != nil {
		tb.Skipf("跳过真实依赖测试: %v", err)
	}

	m, err := memhop.OpenMulti(cfg)
	if err != nil {
		tb.Fatalf("memhop.OpenMulti: %v", err)
	}
	id, err := m.CreateAgent("test")
	if err != nil {
		m.Close()
		tb.Fatalf("CreateAgent: %v", err)
	}
	sess, err := m.Session(id)
	if err != nil {
		m.Close()
		tb.Fatalf("Session: %v", err)
	}
	return &Handle{Session: sess, m: m}
}
