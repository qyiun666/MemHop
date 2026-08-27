// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// llm_keywords.go: semantic keyword extraction — the Search/Update
// preprocessing call point.

package llmops

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

const (
	keywordExtractionMaxTokens = 512
	// Retry budget: reasoning tokens count toward completion_tokens and can
	// exhaust the 512-token first attempt, leaving content empty.
	keywordRetryMaxTokens = 4096
	// keywordChunkRunes is the input-length threshold (in runes) above
	// which extraction splits the text first: prompt constraint weakens
	// with input length, and long inputs are the main trigger of
	// natural-language-summary replies.
	keywordChunkRunes = 2000
)

const systemKeywords = `You compress text by extracting all meaningful keywords and phrases, removing noise while preserving full meaning and tone.

Rules:
1. Semantic completeness — the extracted items, read together, must let a reader understand the original text's facts, intent, relationships, and emotional tone as if reading the original
2. Retrieval-oriented — think: "What words or phrases would someone use to find this content later?" Extract those exact terms
3. Include: named entities (people, places, orgs, products), specific topics, key actions with their objects, time expressions, locations, numbers, cause-effect relationships, emotional tone and attitude markers
4. Use both individual keywords AND short phrases — phrases preserve context that single words lose (e.g. "cancel picnic due to rain" is more meaningful than just "rain" + "picnic")
5. No limit on count — extract everything meaningful; for long text, extract proportionally more; never truncate or summarize — only remove noise (greetings, filler, repetition)
6. Colloquial variants — for important terms, include common synonyms or colloquial alternatives (e.g. "肚子疼" → also add "胃痛"; "bug" → also add "缺陷") so different phrasings can match via BM25
7. Keep each entry concise: prefer keywords and short phrases, avoid full sentences
8. Preserve original language; keep mixed-language terms as-is; preserve numbers and proper nouns exactly
9. Exclude: greetings, filler words, question words (when/where/what/why/how/who/which), generic verbs (go/do/get/run/make/have/want/like/think) unless tied to a specific action
10. Output ONLY valid JSON: {"keywords":[...]}, no markdown, no code fences`

// keywordFormatRetry is appended to the user prompt for the format-constrained
// retry: long inputs drift toward natural-language summaries, and restating
// the hard JSON-only constraint measurably improves structured replies.
const keywordFormatRetry = `

Output ONLY valid JSON: {"keywords":["keyword1", "keyword2", ...]}.
No markdown, no code fences, no explanations.`

// ExtractKeywords extracts semantic keywords whose union represents the
// text's core meaning (unlimited count). Parse failures self-heal: one
// format-constrained retry, then heuristic tokenization, then an empty
// list — Search/Update must never fail because of this preprocessing step.
// Long inputs are chunked first so the JSON constraint stays effective.
// Only the LLM call itself failing surfaces an error.
func ExtractKeywords(ctx context.Context, chat Chat, text string) ([]string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return []string{}, nil
	}
	if utf8.RuneCountInString(trimmed) > keywordChunkRunes {
		return extractKeywordsChunked(ctx, chat, trimmed)
	}
	return extractKeywordsWithRetry(ctx, chat, trimmed)
}

// extractKeywordsWithRetry runs the single-pass path: token budgets for
// truncation, then one format-constrained retry, then heuristic fallback.
func extractKeywordsWithRetry(ctx context.Context, chat Chat, trimmed string) ([]string, error) {
	user := "Extract keywords from:\n" + trimmed
	// Reasoning models consume part of max_tokens for reasoning; on
	// truncation (finish_reason=length) retry with the next larger budget.
	// The final budget is the full consolidation ceiling, bypassing the
	// configured cap exactly like Consolidate's retry does.
	budgets := []int{
		minTokens(chat.MaxOutputTokens(), keywordExtractionMaxTokens),
		minTokens(chat.MaxOutputTokens(), keywordRetryMaxTokens),
		ConsolidationMaxTokens,
	}
	var lastRaw string
	for attempt, maxTokens := range budgets {
		response, err := chat.Chat(ctx, systemKeywords, user, maxTokens, 0.0, 1.0)
		if err != nil {
			if errors.Is(err, common.ErrTruncated) {
				if attempt < len(budgets)-1 {
					continue // escalate to the next larger budget
				}
				break // even the ceiling truncated: fall through to retry+degrade
			}
			return nil, err
		}
		lastRaw = response
		if keywords, ok := parseKeywords(response); ok {
			return dedupeKeywords(keywords), nil
		}
		// Invalid JSON: a truncated response may parse on a larger budget,
		// so keep iterating; format failures are handled after the loop.
	}
	// Every budget failed to parse: one format-constrained retry before
	// degrading, so long-input summaries still get a structured attempt.
	response, err := chat.Chat(ctx, systemKeywords, user+keywordFormatRetry, ConsolidationMaxTokens, 0.0, 1.0)
	if err != nil {
		// The second-chance call failed (transient); degrade instead of
		// aborting the host's search round.
		slog.Warn("llm: keyword extraction retry failed, degrading", "err", err)
		return fallbackKeywords(trimmed, lastRaw), nil
	}
	if keywords, ok := parseKeywords(response); ok {
		return dedupeKeywords(keywords), nil
	}
	return fallbackKeywords(trimmed, response), nil
}

// extractKeywordsChunked extracts per chunk (one attempt each, retry-level
// budget) and merges the results. Unparseable chunks are dropped with a
// warning; when every chunk fails, the whole text degrades via heuristic
// fallback. LLM call failures still surface as errors.
func extractKeywordsChunked(ctx context.Context, chat Chat, trimmed string) ([]string, error) {
	chunks := splitForExtraction(trimmed, keywordChunkRunes)
	merged := make([]string, 0, len(chunks)*4)
	for i, chunk := range chunks {
		response, err := chat.Chat(ctx, systemKeywords, "Extract keywords from:\n"+chunk,
			minTokens(chat.MaxOutputTokens(), keywordRetryMaxTokens), 0.0, 1.0)
		if err != nil {
			return nil, err
		}
		if keywords, ok := parseKeywords(response); ok {
			merged = append(merged, keywords...)
		} else {
			slog.Warn("llm: keyword extraction chunk parse failed, skipped",
				"chunk", i, "raw", common.SafeCharSlice(response, 120))
		}
	}
	merged = dedupeKeywords(merged)
	if len(merged) == 0 {
		return fallbackKeywords(trimmed, ""), nil
	}
	return merged, nil
}

// splitForExtraction splits text into chunks of at most limit runes,
// preferring sentence/line boundaries in the trailing window so keywords
// survive the split; cuts hard at the limit otherwise.
func splitForExtraction(text string, limit int) []string {
	runes := []rune(text)
	if len(runes) <= limit {
		return []string{text}
	}
	chunks := make([]string, 0, (len(runes)+limit-1)/limit)
	for start := 0; start < len(runes); {
		end := start + limit
		if end >= len(runes) {
			chunks = append(chunks, string(runes[start:]))
			break
		}
		window := runes[start:end]
		cut := limit
		for i := len(window) - 1; i >= len(window)/2; i-- {
			if window[i] == '\n' || window[i] == '。' || window[i] == '；' || window[i] == ';' || window[i] == '.' {
				cut = i
				break
			}
		}
		chunks = append(chunks, string(runes[start:start+cut]))
		start += cut
	}
	return chunks
}

// parseKeywords parses the LLM keyword reply; ok=false means the response
// is not valid JSON.
func parseKeywords(response string) ([]string, bool) {
	var raw struct {
		Keywords []string `json:"keywords"`
	}
	if err := json.Unmarshal([]byte(stripCodeBlocks(response)), &raw); err != nil {
		return nil, false
	}
	return raw.Keywords, true
}

// fallbackKeywords degrades keyword extraction: heuristic tokenization
// first (index-side pipeline, matching the BM25 vocabulary), then an empty
// list so retrieval falls back to vector/BM25 over the raw query. Never
// returns an error.
func fallbackKeywords(text, raw string) []string {
	if kw := heuristicKeywords(text); len(kw) > 0 {
		slog.Warn("llm: keywords parse failed, fell back to heuristic keywords", "raw", common.SafeCharSlice(raw, 120))
		return kw
	}
	slog.Warn("llm: keywords parse failed, proceeding without keywords", "raw", common.SafeCharSlice(raw, 120))
	return []string{}
}

// heuristicKeywords tokenizes the source text as fallback keywords.
func heuristicKeywords(text string) []string {
	return index.Tokenize(text)
}

func dedupeKeywords(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
