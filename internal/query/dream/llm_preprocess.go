// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"strings"

	"github.com/qyiun666/MemHop/internal/common/config"
)

// ChatProvider is the interface for LLM chat completions.
type ChatProvider interface {
	Chat(system, user string, maxTokens int, temperature, topP float32) (string, error)
}

// BuildChatProvider returns a ChatProvider if LLM is configured, nil otherwise.
func BuildChatProvider(cfg *config.LlmConfig) ChatProvider {
	if cfg == nil || cfg.APIURL == "" || cfg.APIKey == "" {
		return nil
	}
	return NewOpenAIProvider(cfg)
}

// callLLMWithRetry calls the LLM with a single attempt.
func callLLMWithRetry(
	llm ChatProvider, system, user string,
	maxTokens int, temperature float32,
) (string, error) {
	response, err := llm.Chat(system, user, maxTokens, temperature, 0.85)
	if err != nil {
		return "", err
	}
	return response, nil
}

// stripCodeBlocksLLM removes markdown code block fencing from LLM responses.
func stripCodeBlocksLLM(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	stripped := trimmed[3:]
	start := strings.IndexByte(stripped, '\n')
	if start >= 0 {
		stripped = stripped[start+1:]
	}
	end := strings.LastIndex(stripped, "```")
	if end >= 0 {
		stripped = stripped[:end]
	}
	return strings.TrimSpace(stripped)
}

// filterEmptyStrings removes empty/whitespace-only strings from a slice.
func filterEmptyStrings(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
