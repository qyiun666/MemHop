// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// llm_ops.go hosts the Provider's three LLM call points: keyword extraction,
// L2 consolidation and L1→L0 distillation.

package internal

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// Per-call output caps. Config.MaxOutputTokens is a global ceiling; these
// caps keep the hot path cheap while leaving Dream enough room for its
// context-bearing output.
const (
	keywordExtractionMaxTokens = 512
	// Retry budget: reasoning tokens count toward completion_tokens and can
	// exhaust the 512-token first attempt, leaving content empty.
	keywordRetryMaxTokens = 4096
	// L2 consolidation output becomes host context: keep it generous rather
	// than risk truncating merged summaries for large scenes.
	consolidationMaxTokens = 8192
	distillMaxTokens       = 2048
	// keywordChunkRunes is the input-length threshold (in runes) above
	// which extraction splits the text first: prompt constraint weakens
	// with input length, and long inputs are the main trigger of
	// natural-language-summary replies.
	keywordChunkRunes = 2000
)

func minTokens(configured, cap int) int {
	if configured <= 0 || configured > cap {
		return cap
	}
	return configured
}

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
func (p *Provider) ExtractKeywords(ctx context.Context, text string) ([]string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return []string{}, nil
	}
	if utf8.RuneCountInString(trimmed) > keywordChunkRunes {
		return p.extractKeywordsChunked(ctx, trimmed)
	}
	return p.extractKeywordsWithRetry(ctx, trimmed)
}

// extractKeywordsWithRetry runs the single-pass path: token budgets for
// truncation, then one format-constrained retry, then heuristic fallback.
func (p *Provider) extractKeywordsWithRetry(ctx context.Context, trimmed string) ([]string, error) {
	user := "Extract keywords from:\n" + trimmed
	// Reasoning models consume part of max_tokens for reasoning; on
	// truncation (finish_reason=length) retry with the next larger budget.
	// The final budget is the full consolidation ceiling, bypassing the
	// configured cap exactly like Consolidate's retry does.
	budgets := []int{
		minTokens(p.maxOutputTokens, keywordExtractionMaxTokens),
		minTokens(p.maxOutputTokens, keywordRetryMaxTokens),
		consolidationMaxTokens,
	}
	var lastRaw string
	for attempt, maxTokens := range budgets {
		response, err := p.chat(ctx, systemKeywords, user, maxTokens, 0.0, 1.0)
		if err != nil {
			if attempt < len(budgets)-1 && strings.Contains(err.Error(), "truncated") {
				continue
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
	response, err := p.chat(ctx, systemKeywords, user+keywordFormatRetry, consolidationMaxTokens, 0.0, 1.0)
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
func (p *Provider) extractKeywordsChunked(ctx context.Context, trimmed string) ([]string, error) {
	chunks := splitForExtraction(trimmed, keywordChunkRunes)
	merged := make([]string, 0, len(chunks)*4)
	for i, chunk := range chunks {
		response, err := p.chat(ctx, systemKeywords, "Extract keywords from:\n"+chunk,
			minTokens(p.maxOutputTokens, keywordRetryMaxTokens), 0.0, 1.0)
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

type L2Group struct {
	SceneID       uint64   `json:"scene_id"`
	NodeHashes    []uint64 `json:"node_hashes"`
	MergedSummary string   `json:"merged_summary"`
}

type ConsolidationOutput struct {
	L2Groups            []L2Group `json:"l2_groups"`
	L2CompressionNeeded bool      `json:"l2_compression_needed"`
}

const systemConsolidate = `You analyze L2 chat memory topics, identify which adjacent topics belong to the same conversation thread, and reconstruct their keywords into natural text that reads like the original conversation.

Rules:
1. Scan from the most recent topic backwards — group adjacent topics that share the same conversation thread (same subject, causal chain, topic continuation, or semantic overlap)
2. Do NOT merge topics that are clearly about different subjects, even if they occur in the same scene
3. Compression target: the total number of remaining topics after merging must be 20 or fewer. If the input has more than 20 topics, you MUST merge enough groups to bring the count down to 20 or below
4. For each merged group, reconstruct the multi-turn keywords into a single coherent natural-language text — write it as if you are rewriting what was originally said, not summarizing. The keywords are a fact checklist: every keyword or phrase from every topic in the group MUST appear in the reconstructed text, either verbatim or as the exact fact it stands for. Never drop, merge away, or generalize a fact (e.g. "yesterday" must stay "yesterday", not become "recently")
5. No length limit on the reconstructed text — it must be as long as needed to faithfully preserve ALL details: names, numbers, dates, times (including relative references such as "yesterday", "last week", "next month" — keep them exactly as said), locations, cause-effect chains, emotional tone, attitudes, preferences, and specific facts
6. Preserve the emotional tone and attitude present in the keywords — if the original tone was frustrated, excited, curious, etc., the reconstructed text should reflect that
7. Preserve original language; keep mixed-language terms as-is; preserve numbers and proper nouns exactly
8. Echo scene_id and node_hashes EXACTLY as given in the input
9. When no compression is needed (no adjacent topics share a thread, or total topics already <= 20), output l2_groups as an empty array

Output ONLY valid JSON in this exact shape (no markdown, no code fences):
{
  "l2_groups": [
    {
      "scene_id": <number>,
      "node_hashes": [<number>, ...],
      "merged_summary": "<natural-language reconstruction of the original conversation from the keywords>"
    }
  ],
  "l2_compression_needed": <bool>
}
Every merged group MUST include non-empty merged_summary.`

// Consolidate decides whether a batch of L2 topics share a topic and
// returns compression groups preserving all details.
func (p *Provider) Consolidate(ctx context.Context, topics []core.TopicSlot) (*ConsolidationOutput, error) {
	if len(topics) == 0 {
		return &ConsolidationOutput{L2Groups: []L2Group{}, L2CompressionNeeded: false}, nil
	}
	user := buildConsolidatePrompt(topics)
	// Two-budget attempt: the first pass uses the configured ceiling; when
	// the response is truncated (finish_reason=length, common with reasoning
	// models), retry once with the full consolidation budget so merged
	// summaries are never cut mid-JSON.
	response, err := p.chatWithRetry(ctx, systemConsolidate, user, minTokens(p.maxOutputTokens, consolidationMaxTokens), consolidationMaxTokens)
	if err != nil {
		return nil, err
	}
	return parseConsolidateResponse(response)
}

// chatWithRetry runs chat with a primary max-token budget; if the response
// is truncated by the token ceiling, retries once with the retry budget.
// Non-truncation errors are returned immediately.
func (p *Provider) chatWithRetry(ctx context.Context, system, user string, primaryMax, retryMax int) (string, error) {
	response, err := p.chat(ctx, system, user, primaryMax, 0.0, 1.0)
	if err == nil || !strings.Contains(err.Error(), "truncated") || primaryMax >= retryMax {
		return response, err
	}
	return p.chat(ctx, system, user, retryMax, 0.0, 1.0)
}

// buildConsolidatePrompt lists topics grouped by scene, sorted by user turn
// time (adjacency matters for merge judgment).
func buildConsolidatePrompt(topics []core.TopicSlot) string {
	byScene := make(map[uint64][]core.TopicSlot)
	for _, t := range topics {
		byScene[t.SceneID] = append(byScene[t.SceneID], t)
	}
	sceneIDs := make([]uint64, 0, len(byScene))
	for sid := range byScene {
		sceneIDs = append(sceneIDs, sid)
	}
	slices.Sort(sceneIDs)

	var b strings.Builder
	fmt.Fprintf(&b, "# L2 Topic Data (%d scenes)\n\n", len(sceneIDs))
	for _, sid := range sceneIDs {
		nodes := byScene[sid]
		slices.SortStableFunc(nodes, func(a, b core.TopicSlot) int {
			if a.UserTimestamp != b.UserTimestamp {
				return cmp.Compare(a.UserTimestamp, b.UserTimestamp)
			}
			return cmp.Compare(a.ID, b.ID)
		})
		fmt.Fprintf(&b, "## scene_id = %d\n", sid)
		for _, n := range nodes {
			fmt.Fprintf(&b, "- id=%d depth=%d user_kw=%v agent_kw=%v fused_kw=%v\n",
				n.ID, n.Depth, n.UserKeywords, n.AgentKeywords, n.FusedKeywords)
		}
		b.WriteByte('\n')
	}
	b.WriteString("Decide which topics belong to the same topic and output the merged groups now.")
	return b.String()
}

// parseConsolidateResponse parses the LLM reply; scene_id/node_hashes
// accept JSON numbers or quoted strings.
func parseConsolidateResponse(response string) (*ConsolidationOutput, error) {
	cleaned := stripCodeBlocks(response)
	var raw struct {
		L2Groups []struct {
			SceneID       json.RawMessage   `json:"scene_id"`
			NodeHashes    []json.RawMessage `json:"node_hashes"`
			MergedSummary string            `json:"merged_summary"`
		} `json:"l2_groups"`
		L2CompressionNeeded bool `json:"l2_compression_needed"`
	}
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, common.NewError(common.ErrLLM, "consolidate response parse failed", err)
	}
	out := &ConsolidationOutput{
		L2Groups:            make([]L2Group, 0, len(raw.L2Groups)),
		L2CompressionNeeded: raw.L2CompressionNeeded,
	}
	for _, g := range raw.L2Groups {
		sceneID, err := parseUint64Flex(g.SceneID)
		if err != nil {
			return nil, common.NewError(common.ErrLLM, "consolidate scene_id parse failed", err)
		}
		hashes := make([]uint64, 0, len(g.NodeHashes))
		for _, h := range g.NodeHashes {
			v, err := parseUint64Flex(h)
			if err != nil {
				return nil, common.NewError(common.ErrLLM, "consolidate node_hashes parse failed", err)
			}
			hashes = append(hashes, v)
		}
		out.L2Groups = append(out.L2Groups, L2Group{
			SceneID: sceneID, NodeHashes: hashes,
			MergedSummary: g.MergedSummary,
		})
	}
	return out, nil
}

// parseUint64Flex parses a JSON number or quoted string as uint64, decimal
// first then hex (0x prefix).
func parseUint64Flex(raw json.RawMessage) (uint64, error) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return 0, common.NewError(common.ErrLLM, "empty uint64 value")
	}
	if v, err := strconv.ParseUint(s, 10, 64); err == nil {
		return v, nil
	}
	return strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
}

// L1Sample is a distill input assembled from an L1 node and its topics
// (keywords come from linked L2 topics).
type L1Sample struct {
	IDHash     uint64
	Keywords   []string
	Summary    string
	Importance float32
	Depth      uint8
}

type EmotionScore struct {
	Valence   float64 `json:"valence"`
	Arousal   float64 `json:"arousal"`
	Dominance float64 `json:"dominance"`
}

// MBTIScore holds four MBTI dimensions in [-1,1]; Type is derived from the dimensions.
type MBTIScore struct {
	IE   float64 `json:"i_e"`
	NS   float64 `json:"n_s"`
	TF   float64 `json:"t_f"`
	JP   float64 `json:"j_p"`
	Type string  `json:"type"`
}

type NodeEmotion struct {
	IDHex   string  `json:"id_hex"`
	Valence float64 `json:"valence"`
	Arousal float64 `json:"arousal"`
}

type DistillOutput struct {
	Emotion EmotionScore
	MBTI    MBTIScore
	PerNode []NodeEmotion
}

const systemDistill = `You analyze an AI agent's L1 associative memory samples and derive its current emotional state and MBTI-style personality.

Output ONLY a JSON object:
{
  "emotion": {"valence": 0.0..1.0, "arousal": 0.0..1.0, "dominance": 0.0..1.0},
  "mbti": {"i_e": -1.0..1.0, "n_s": -1.0..1.0, "t_f": -1.0..1.0, "j_p": -1.0..1.0, "type": "XXXX"},
  "per_node": [{"id_hex": "16-hex-digits", "valence": 0.0..1.0, "arousal": 0.0..1.0}]
}

Rules:
- valence: 0=very negative, 1=very positive
- arousal: 0=calm, 1=highly excited
- dominance: 0=submissive, 1=dominant
- MBTI dimensions: negative = I/N/T/J, positive = E/S/F/P; magnitude = strength
- per_node only for nodes with a clear emotional signal (skip neutral ones)
- No markdown, no code fences, no commentary — JSON only`

// Distill derives emotional state and MBTI profile from L1 node samples
// for L0 profile merging.
func (p *Provider) Distill(ctx context.Context, samples []L1Sample) (*DistillOutput, error) {
	if len(samples) == 0 {
		return nil, common.NewError(common.ErrLLM, "distill: no samples")
	}
	user := buildDistillPrompt(samples)
	// Same truncation-retry as Consolidate: reasoning tokens can exhaust the
	// 2048 first-pass budget, cutting the JSON mid-stream.
	response, err := p.chatWithRetry(ctx, systemDistill, user, minTokens(p.maxOutputTokens, distillMaxTokens), consolidationMaxTokens)
	if err != nil {
		return nil, err
	}
	return parseDistillResponse(response)
}

func buildDistillPrompt(samples []L1Sample) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# L1 samples (%d)\n\n", len(samples))
	for _, s := range samples {
		fmt.Fprintf(&b, "- id_hex=%s importance=%.3f depth=%d kw=%v\n",
			common.FormatHash(s.IDHash), s.Importance, s.Depth, s.Keywords)
		if s.Summary != "" {
			fmt.Fprintf(&b, "  summary: %s\n", s.Summary)
		}
	}
	b.WriteString("\nOutput the JSON now.")
	return b.String()
}

func parseDistillResponse(response string) (*DistillOutput, error) {
	cleaned := stripCodeBlocks(response)
	var raw struct {
		Emotion EmotionScore  `json:"emotion"`
		MBTI    MBTIScore     `json:"mbti"`
		PerNode []NodeEmotion `json:"per_node"`
	}
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, common.NewError(common.ErrLLM, "distill response parse failed", err)
	}
	out := &DistillOutput{
		Emotion: EmotionScore{
			Valence:   clampUnit(raw.Emotion.Valence),
			Arousal:   clampUnit(raw.Emotion.Arousal),
			Dominance: clampUnit(raw.Emotion.Dominance),
		},
		MBTI: MBTIScore{
			IE: clampSigned(raw.MBTI.IE),
			NS: clampSigned(raw.MBTI.NS),
			TF: clampSigned(raw.MBTI.TF),
			JP: clampSigned(raw.MBTI.JP),
		},
		PerNode: make([]NodeEmotion, 0, len(raw.PerNode)),
	}
	// Type re-derived from the four dimensions (LLM type field not trusted).
	out.MBTI.Type = deriveMBTIType(out.MBTI)
	for _, n := range raw.PerNode {
		if _, err := common.ParseID(n.IDHex); err != nil {
			continue // skip rows with unparsable ids
		}
		out.PerNode = append(out.PerNode, NodeEmotion{
			IDHex: n.IDHex, Valence: clampUnit(n.Valence), Arousal: clampUnit(n.Arousal),
		})
	}
	return out, nil
}

func clampUnit(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampSigned(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

func deriveMBTIType(m MBTIScore) string {
	pick := func(v float64, neg, pos byte) byte {
		if v < 0 {
			return neg
		}
		return pos
	}
	return string([]byte{
		pick(m.IE, 'I', 'E'),
		pick(m.NS, 'N', 'S'),
		pick(m.TF, 'T', 'F'),
		pick(m.JP, 'J', 'P'),
	})
}

// CrystallizeCapability is one capability candidate extracted from a
// trajectory. Action is create, reuse or merge.
type CrystallizeCapability struct {
	Action     string           `json:"action"`
	ReuseID    string           `json:"reuse_id,omitempty"`
	Capability CapabilityImport `json:"capability"`
}

type CrystallizeOutput struct {
	Capabilities []CrystallizeCapability `json:"capabilities"`
}

const systemCrystallize = `You analyze an agent's operation trajectory and extract reusable L5 capabilities.

Rules:
- Only extract capabilities that are clearly reusable (appear at least twice or are obviously generic procedures)
- A capability has type skill, mcp, api, or composite:
  * skill: a reusable skill/runbook/SOP (ref = skill path or manual reference)
  * mcp: a single reusable tool provided by an MCP server (ref = server address)
  * api: a single reusable host method (ref = "api:MethodName")
  * composite: an orchestration of the above resources, with an optional workflow
- For composite capabilities, list the referenced resources in "resources" and their ordered orchestration in workflow.steps (ref refers to a resources[].name; args carries step parameters). Do not invent tools or services that are not present in the trajectory
- Every resource is a tool declaration: name = tool name, desc = how to call it (for the LLM), input = args JSON Schema string (omit when none), output = output description, ref = server address / skill path / api:Method, config = connection JSON (optional)
- Compare against the existing capabilities listed below. If the same capability already exists:
  * action = "reuse" and reuse_id = its 16-hex id
  * do not duplicate it
- If a candidate is a newer variant of an existing capability, use action = "merge" and reuse_id = its existing id
- Otherwise action = "create"
- When no reusable capability exists, output capabilities as an empty array

Output ONLY valid JSON in this exact shape (no markdown, no code fences):
{
  "capabilities": [
    {
      "action": "create|reuse|merge",
      "reuse_id": "16-hex-id when action is reuse or merge, otherwise omit",
      "capability": {
        "name": "<short capability name>",
        "version": "1",
        "type": "skill|mcp|api|composite",
        "summary": "<one sentence>",
        "trigger": "<when this capability applies>",
        "resources": [
          {"type": "skill|mcp|api", "name": "<tool name>", "desc": "<how to call it, for the LLM>", "input": "<args JSON Schema string, omit when none>", "output": "<output description>", "ref": "<server address / skill path / api:Method>", "config": "<connection JSON, optional>"}
        ],
        "workflow": {"steps": [{"ref": "<resources[].name>", "action": "<what this step does>", "args": {"<param>": "<value>"}}]}
      }
    }
  ]
}`

// Crystallize extracts reusable L5 capabilities from a trajectory event
// batch. Existing capabilities are included in the prompt so the model can
// reuse or merge instead of duplicating.
func (p *Provider) Crystallize(ctx context.Context, events []core.TrajectorySlot, existing []core.Capability) (*CrystallizeOutput, error) {
	if len(events) == 0 {
		return &CrystallizeOutput{Capabilities: []CrystallizeCapability{}}, nil
	}
	user := buildCrystallizePrompt(events, existing)
	response, err := p.chat(ctx, systemCrystallize, user, p.maxOutputTokens, 0.0, 1.0)
	if err != nil {
		return nil, err
	}
	return parseCrystallizeResponse(response)
}

// buildCrystallizePrompt lists trajectory events followed by existing L5
// capability prompt cards.
func buildCrystallizePrompt(events []core.TrajectorySlot, existing []core.Capability) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Operation Trajectory (%d events)\n\n", len(events))
	for _, ev := range events {
		fmt.Fprintf(&b, "[seq=%d type=%s] %s\n", ev.Seq, ev.EventType, ev.Payload)
	}
	b.WriteString("\n# Existing L5 capabilities\n")
	if len(existing) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, cap := range existing {
			b.WriteString(cap.PromptCard())
			b.WriteByte('\n')
		}
	}
	b.WriteString("\nExtract reusable capabilities now.")
	return b.String()
}

// parseCrystallizeResponse parses the LLM reply, dropping malformed rows.
func parseCrystallizeResponse(response string) (*CrystallizeOutput, error) {
	cleaned := stripCodeBlocks(response)
	var raw struct {
		Capabilities []struct {
			Action     string           `json:"action"`
			ReuseID    string           `json:"reuse_id,omitempty"`
			Capability CapabilityImport `json:"capability"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, common.NewError(common.ErrLLM, "crystallize response parse failed", err)
	}
	out := &CrystallizeOutput{Capabilities: make([]CrystallizeCapability, 0, len(raw.Capabilities))}
	for _, c := range raw.Capabilities {
		if strings.TrimSpace(c.Capability.Name) == "" {
			continue
		}
		action := strings.ToLower(strings.TrimSpace(c.Action))
		if action == "" {
			action = "create"
		}
		if action != "create" && action != "reuse" && action != "merge" {
			continue
		}
		out.Capabilities = append(out.Capabilities, CrystallizeCapability{
			Action: action, ReuseID: c.ReuseID, Capability: c.Capability,
		})
	}
	return out, nil
}
