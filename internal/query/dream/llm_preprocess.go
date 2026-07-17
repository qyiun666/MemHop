// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"encoding/json"
	"strings"

	"memhop/internal/common/config"
	"memhop/internal/common/mherrors"
)

// ChatProvider is the interface for LLM chat completions used by preprocessing.
type ChatProvider interface {
	Chat(system, user string, maxTokens int, temperature, topP float32) (string, error)
}

// ============================================================================
// System prompts
// ============================================================================

const systemSearchPreprocess = `You extract search keywords from queries. Output JSON only.

Examples:
{"keywords":["Tokyo","days","trip","how long"], "needs_l3_import":false}
{"keywords":["Python","FastAPI","performance","benchmark"], "needs_l3_import":true}

Rules:
- Preserve all proper nouns, numbers, technical terms
- Keep bilingual terms as-is (e.g. "Docker容器", "JWT认证")
- Keywords should be precise enough for BM25 + vector search`

// ============================================================================
// Internal JSON structures for deserialization
// ============================================================================

type searchPreprocessJSON struct {
	Keywords      []string `json:"keywords"`
	NeedsL3Import bool     `json:"needs_l3_import"`
	L3Entities    []struct {
		Name       string `json:"name"`
		EntityType string `json:"type"`
	} `json:"l3_entities"`
}

// ============================================================================
// BuildChatProvider — constructs a ChatProvider from config
// ============================================================================

// BuildChatProvider returns a ChatProvider if LLM is configured, nil otherwise.
func BuildChatProvider(cfg *config.LlmConfig) ChatProvider {
	if cfg == nil || cfg.APIURL == "" || cfg.APIKey == "" {
		return nil
	}
	return NewOpenAIProvider(cfg)
}

// ============================================================================
// Public API — Search preprocess
// ============================================================================

// PreprocessSearchQuery preprocesses a search query with LLM for optimized
// keyword extraction and L3 import judgment.
// Returns an error if the LLM call or response parsing fails.
func PreprocessSearchQuery(llm ChatProvider, q string) (*SearchPreprocessResult, error) {
	userPrompt := buildSearchPrompt(q)
	trimmed := strings.TrimSpace(q)
	if trimmed == "" || trimmed == "?" || trimmed == "？" {
		return &SearchPreprocessResult{Keywords: []string{q}}, nil
	}
	response, err := callLLMWithRetry(llm, systemSearchPreprocess, userPrompt, 2048, 0.0)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrLLM, "search preprocess LLM call failed", err)
	}
	if strings.TrimSpace(response) == "" {
		return &SearchPreprocessResult{Keywords: []string{q}}, nil
	}
	result, err := parseSearchResponse(response, q)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrLLM, "search preprocess LLM response parse failed", err)
	}
	return result, nil
}

// ============================================================================
// Internal helpers
// ============================================================================

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

func buildSearchPrompt(q string) string {
	return "# Search Preprocessing\n\nOriginal user query:\n" + q + "\n\nExtract keywords and decide L3 import. Output JSON."
}

func parseSearchResponse(response, originalQuery string) (*SearchPreprocessResult, error) {
	cleaned := stripCodeBlocksLLM(response)
	var raw searchPreprocessJSON
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, mherrors.NewError(mherrors.ErrDeserialization, "parse search preprocess", err)
	}
	keywords := filterEmptyStrings(raw.Keywords)
	result := &SearchPreprocessResult{
		Keywords:      keywords,
		NeedsL3Import: raw.NeedsL3Import,
	}
	// Convert L3 entities to hints
	for _, e := range raw.L3Entities {
		result.L3Entities = append(result.L3Entities, L3EntityHint{
			Name:       e.Name,
			EntityType: e.EntityType,
		})
	}
	return result, nil
}

func filterEmptyStrings(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

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
