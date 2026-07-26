// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package testsupport

import (
	"errors"
	"os"
	"testing"

	"github.com/qyiun666/MemHop/api"
)

// 环境变量提供完整 LLM 配置时优先于 key_config.json 文件。
func TestLoadLLMConfigEnvPriority(t *testing.T) {
	t.Setenv(EnvLLMKey, "sk-env-test-key")
	t.Setenv(EnvLLMURL, "https://example.com/v1/chat/completions")
	t.Setenv(EnvLLMModel, "env-model")

	var cfg memhop.Config
	if err := loadLLMConfig(&cfg); err != nil {
		t.Fatalf("loadLLMConfig: %v", err)
	}
	if cfg.LLM.APIKey != "sk-env-test-key" {
		t.Errorf("APIKey = %q, want env value", cfg.LLM.APIKey)
	}
	if cfg.LLM.APIURL != "https://example.com/v1/chat/completions" {
		t.Errorf("APIURL = %q, want env value", cfg.LLM.APIURL)
	}
	if cfg.LLM.Model != "env-model" {
		t.Errorf("Model = %q, want env value", cfg.LLM.Model)
	}
}

// 只设置 key 时，URL/Model 使用与 key_config.json.example 一致的默认值。
func TestLoadLLMConfigEnvDefaults(t *testing.T) {
	t.Setenv(EnvLLMKey, "sk-env-test-key")
	t.Setenv(EnvLLMURL, "")
	t.Setenv(EnvLLMModel, "")

	var cfg memhop.Config
	if err := loadLLMConfig(&cfg); err != nil {
		t.Fatalf("loadLLMConfig: %v", err)
	}
	if cfg.LLM.APIURL != defaultLLMURL {
		t.Errorf("APIURL = %q, want default %q", cfg.LLM.APIURL, defaultLLMURL)
	}
	if cfg.LLM.Model != defaultLLMModel {
		t.Errorf("Model = %q, want default %q", cfg.LLM.Model, defaultLLMModel)
	}
}

// 无环境变量时：存在 key_config.json 则从文件加载；两者都缺失必须返回
// errNoLLMConfig（调用方据此 t.Skip，而不是 panic）。
func TestLoadLLMConfigFallback(t *testing.T) {
	t.Setenv(EnvLLMKey, "")

	var cfg memhop.Config
	err := loadLLMConfig(&cfg)
	if _, statErr := os.Stat(keyConfigPath()); statErr == nil {
		if err != nil {
			t.Fatalf("key_config.json 存在但加载失败: %v", err)
		}
		if cfg.LLM.APIKey == "" {
			t.Error("key_config.json 存在但 APIKey 为空")
		}
		return
	}
	if !errors.Is(err, errNoLLMConfig) {
		t.Errorf("err = %v, want errNoLLMConfig", err)
	}
}
