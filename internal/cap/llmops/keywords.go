// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// llm_keywords.go: semantic keyword extraction — the write-path preprocessing
// call point (one finished turn in, one keyword track out).

package llmops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/qyiun666/MemHop/internal/common"
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
6. Colloquial variants — for important terms, include common synonyms or colloquial alternatives (e.g. "肚子疼" → also add "胃痛"; "bug" → also add "缺陷") so one idea is not split across differently worded turns
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

// errKeywordFormat is what extraction reports when every attempt — including
// the format-constrained retry — came back without parseable JSON.
var errKeywordFormat = common.NewError(common.ErrLLM,
	"keyword extraction returned no parseable JSON; check the model's structured-output capability")

// ExtractKeywords extracts semantic keywords whose union represents the
// text's core meaning (unlimited count). A reply that is not valid JSON gets
// one format-constrained retry and then surfaces as an error: the keyword
// track is what a host reads back as its conversation context, so a degraded
// or empty track would be written into the topic as if it were the real one.
// Long inputs are chunked first so the JSON constraint stays effective.
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

// ExtractTurnKeywords distills one finished turn into the single keyword
// track of its topic. The speaker labels matter: without them the two sides
// of an exchange collapse into one undifferentiated text and the extraction
// loses who asserted what.
func ExtractTurnKeywords(ctx context.Context, chat Chat, userText, agentText string) ([]string, error) {
	return ExtractKeywords(ctx, chat, "User: "+userText+"\nAssistant: "+agentText)
}

// extractOne runs the full attempt ladder for one prompt: escalating token
// budgets (a reasoning model can spend the first budget on reasoning and
// truncate the reply), then one format-constrained retry that restates the
// JSON-only rule. A transport failure surfaces as itself; a model that never
// answered in JSON yields errKeywordFormat.
//
// Every unit of extraction goes through here — a whole text and each of its
// chunks alike. Hardening only the short-input path left long inputs the
// weaker one, which is backwards: the longer the text, the more readily a
// model drifts into a natural-language summary.
func extractOne(ctx context.Context, chat Chat, user string) ([]string, error) {
	budgets := []int{
		minTokens(chat.MaxOutputTokens(), keywordExtractionMaxTokens),
		minTokens(chat.MaxOutputTokens(), keywordRetryMaxTokens),
		ConsolidationMaxTokens,
	}
	for _, maxTokens := range budgets {
		response, err := chat.Chat(ctx, systemKeywords, user, maxTokens, 0.0, 1.0)
		if err != nil {
			if errors.Is(err, common.ErrTruncated) {
				continue // a larger budget may fit the whole reply
			}
			return nil, err
		}
		if keywords, ok := parseKeywords(response); ok {
			return dedupeKeywords(keywords), nil
		}
	}
	response, err := chat.Chat(ctx, systemKeywords, user+keywordFormatRetry, ConsolidationMaxTokens, 0.0, 1.0)
	if err != nil {
		if errors.Is(err, common.ErrTruncated) {
			return nil, errKeywordFormat
		}
		return nil, err
	}
	if keywords, ok := parseKeywords(response); ok {
		return dedupeKeywords(keywords), nil
	}
	return nil, errKeywordFormat
}

// extractKeywordsWithRetry runs the single-pass path.
func extractKeywordsWithRetry(ctx context.Context, chat Chat, trimmed string) ([]string, error) {
	return extractOne(ctx, chat, "Extract keywords from:\n"+trimmed)
}

// extractKeywordsChunked extracts per chunk through the same ladder and merges
// the results. A chunk that fails every attempt is an error: the surviving
// chunks would otherwise read as a complete keyword track while silently
// missing one part of the text.
func extractKeywordsChunked(ctx context.Context, chat Chat, trimmed string) ([]string, error) {
	chunks := splitForExtraction(trimmed, keywordChunkRunes)
	merged := make([]string, 0, len(chunks)*4)
	for i, chunk := range chunks {
		keywords, err := extractOne(ctx, chat, "Extract keywords from:\n"+chunk)
		if err != nil {
			if errors.Is(err, errKeywordFormat) {
				return nil, common.NewError(common.ErrLLM,
					fmt.Sprintf("keyword extraction chunk %d returned no parseable JSON", i), err)
			}
			return nil, err
		}
		merged = append(merged, keywords...)
	}
	return dedupeKeywords(merged), nil
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
