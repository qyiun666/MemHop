// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"encoding/json"
	"strings"

	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/query"
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

const systemWritePreprocess = `You extract keywords and importance from dialogue. Output JSON only.

Examples:
{"keywords":["app","build","React","UI"], "importance":0.7}
{"keywords":["weather","sunny","weekend"], "importance":0.2}

Rules:
- Preserve all proper nouns, technical terms, version numbers
- Keep bilingual terms as-is (e.g. "React组件", "状态管理")
- Importance 0.0-0.2 casual, 0.3-0.5 routine, 0.6-0.8 technical, 0.9-1.0 critical`

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

type writePreprocessJSON struct {
	Keywords   []string `json:"keywords"`
	Importance float32  `json:"importance"`
}

// ============================================================================
// Public API — Search preprocess
// ============================================================================

// PreprocessSearchQuery preprocesses a search query with LLM for optimized
// keyword extraction and L3 import judgment.
// Returns an error if the LLM call or response parsing fails.
func PreprocessSearchQuery(llm ChatProvider, q string) (*query.SearchPreprocessResult, error) {
	userPrompt := buildSearchPrompt(q)
	trimmed := strings.TrimSpace(q)
	if trimmed == "" || trimmed == "?" || trimmed == "？" {
		return &query.SearchPreprocessResult{Keywords: []string{q}}, nil
	}
	response, err := callLLMWithRetry(llm, systemSearchPreprocess, userPrompt, 2048, 0.0)
	if err != nil {
		return nil, core.NewError(core.ErrLLM, "search preprocess LLM call failed", err)
	}
	if strings.TrimSpace(response) == "" {
		return &query.SearchPreprocessResult{Keywords: []string{q}}, nil
	}
	result, err := parseSearchResponse(response, q)
	if err != nil {
		return nil, core.NewError(core.ErrLLM, "search preprocess LLM response parse failed", err)
	}
	return result, nil
}

// PreprocessWriteContent preprocesses write content with LLM for keyword
// extraction and importance scoring.
// Returns an error if the LLM call or response parsing fails.
func PreprocessWriteContent(llm ChatProvider, content string) (*query.WritePreprocessResult, error) {
	userPrompt := buildWritePrompt(content)
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return &query.WritePreprocessResult{Keywords: []string{content}, Importance: 0.5}, nil
	}
	response, err := callLLMWithRetry(llm, systemWritePreprocess, userPrompt, 2048, 0.0)
	if err != nil {
		return nil, core.NewError(core.ErrLLM, "write preprocess LLM call failed", err)
	}
	if strings.TrimSpace(response) == "" {
		return &query.WritePreprocessResult{Keywords: []string{""}, Importance: 0.5}, nil
	}
	result, err := parseWriteResponse(response)
	if err != nil {
		return nil, core.NewError(core.ErrLLM, "write preprocess LLM response parse failed", err)
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

func buildWritePrompt(content string) string {
	truncated := content
	runes := []rune(content)
	if len(runes) > 4000 {
		truncated = string(runes[:4000])
	}
	return "# Write Preprocessing\n\nDialogue content:\n" + truncated + "\n\nExtract keywords and importance score. Output JSON."
}

func parseSearchResponse(response, originalQuery string) (*query.SearchPreprocessResult, error) {
	cleaned := stripCodeBlocksLLM(response)
	var raw searchPreprocessJSON
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, core.NewError(core.ErrDeserialization, "parse search preprocess", err)
	}
	keywords := filterEmptyStrings(raw.Keywords)
	result := &query.SearchPreprocessResult{
		Keywords:      keywords,
		NeedsL3Import: raw.NeedsL3Import,
	}
	// Convert L3 entities to hints
	for _, e := range raw.L3Entities {
		result.L3Entities = append(result.L3Entities, query.L3EntityHint{
			Name:       e.Name,
			EntityType: e.EntityType,
		})
	}
	return result, nil
}

func parseWriteResponse(response string) (*query.WritePreprocessResult, error) {
	cleaned := stripCodeBlocksLLM(response)
	var raw writePreprocessJSON
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, core.NewError(core.ErrDeserialization, "parse write preprocess", err)
	}
	keywords := filterEmptyStrings(raw.Keywords)
	importance := raw.Importance
	if importance < 0 {
		importance = 0
	}
	if importance > 1 {
		importance = 1
	}
	return &query.WritePreprocessResult{
		Keywords:   keywords,
		Importance: importance,
	}, nil
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
