// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/qyiun666/memhop/memhop/internal/core"
)

const systemConsolidate = `You are a JSON extraction engine for memory consolidation. Analyze the chat memories and output structured JSON.

Rules:
- Output ONLY valid JSON, no markdown, no code fences, no extra text
- Each field can independently be null (set to null, don't omit)
- Preserve all proper nouns, technical terms, version numbers, and numbers
- Keep mixed-language terms (English+Chinese) in their original form
- Extract concepts based solely on facts in the input, never speculate`

// OpenAIProvider is an OpenAI-compatible LLM provider.
type OpenAIProvider struct {
	apiURL     string
	apiKey     string
	model      string
	timeout    time.Duration
	httpClient *http.Client
}

// NewOpenAIProvider creates an OpenAI provider from config.
func NewOpenAIProvider(cfg *core.LlmConfig) *OpenAIProvider {
	timeoutSecs := cfg.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = 120
	}
	return &OpenAIProvider{
		apiURL:  cfg.APIURL,
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		timeout: time.Duration(timeoutSecs) * time.Second,
		httpClient: &http.Client{
			Timeout: time.Duration(timeoutSecs) * time.Second,
		},
	}
}

// Chat sends a chat completion request with custom parameters.
func (p *OpenAIProvider) Chat(
	system, user string, maxTokens int, temperature, topP float32,
) (string, error) {
	body := map[string]interface{}{
		"model": p.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"max_tokens":        maxTokens,
		"temperature":       temperature,
		"top_p":             topP,
		"presence_penalty":  0.0,
		"frequency_penalty": 0.0,
		"stream":            false,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", core.NewError(core.ErrLLM, "marshal chat request", err)
	}
	req, err := http.NewRequest("POST", p.apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", core.NewError(core.ErrLLM, "create chat request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", core.NewError(core.ErrLLM, "chat api call failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", core.NewError(core.ErrLLM,
			fmt.Sprintf("chat api: %d - %s", resp.StatusCode, string(respBody)))
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", core.NewError(core.ErrSerialization, "parse chat response", err)
	}
	if len(result.Choices) == 0 {
		return "", core.NewError(core.ErrSerialization, "no content in chat response")
	}
	return result.Choices[0].Message.Content, nil
}

// Consolidate performs the LLM consolidation call.
func (p *OpenAIProvider) Consolidate(input *ConsolidationInput) (*ConsolidationOutput, error) {
	data := buildDataSection(input)
	tasks := buildTaskPrompt()
	// Tasks before data: model needs to know WHAT to extract before reading the data
	userPrompt := tasks + "\n\n# Input Data\n\n" + data

	response, err := p.callWithRetry(systemConsolidate, userPrompt)
	if err != nil {
		return nil, err
	}
	return parseConsolidatedResponse(response)
}

// callAPI sends a single chat completion request.
func (p *OpenAIProvider) callAPI(system, user string) (string, error) {
	body := map[string]interface{}{
		"model": p.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"max_tokens":        128000,
		"temperature":        0.0,
		"top_p":              0.0,
		"presence_penalty":   0.0,
		"frequency_penalty":  0.0,
		"stream":             false,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", core.NewError(core.ErrLLM, "marshal request", err)
	}

	req, err := http.NewRequest("POST", p.apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", core.NewError(core.ErrLLM, "create request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", core.NewError(core.ErrLLM, "api call failed", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", core.NewError(core.ErrLLM,
			fmt.Sprintf("api request failed: %d - %s", resp.StatusCode, string(respBody)))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", core.NewError(core.ErrSerialization, "parse response", err)
	}
	if len(result.Choices) == 0 {
		return "", core.NewError(core.ErrSerialization, "no content in response")
	}
	return result.Choices[0].Message.Content, nil
}

func (p *OpenAIProvider) callWithRetry(system, user string) (string, error) {
	response, err := p.callAPI(system, user)
	if err != nil {
		return "", err
	}
	if _, parseErr := parseConsolidatedResponse(response); parseErr == nil {
		return response, nil
	}
	retryPrompt := user + "\n\n" + jsonRetryHint
	return p.callAPI(system, retryPrompt)
}

const jsonRetryHint = "Your previous response could not be parsed. Output ONLY valid JSON with no markdown code blocks or extra text."

func stripCodeBlocks(s string) string {
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

func buildDataSection(input *ConsolidationInput) string {
	var b strings.Builder
	b.WriteString("## L2 Context Data (grouped by scene, nodes sorted by time)\n\n")
	for _, scene := range input.Scenes {
		fmt.Fprintf(&b, "### scene_id = %d\n", scene.SceneID)
		for _, node := range scene.Nodes {
			title := joinStrs(node.FusedKeywords, node.UserKeywords)
			fmt.Fprintf(&b, "- id=%016x  depth=%d  user_kw=%v  agent_kw=%v  title=%q\n",
				node.IDHash, node.Depth, node.UserKeywords, node.AgentKeywords, title)
			if node.FusedSummary != nil {
				summary := *node.FusedSummary
				if len(summary) > 400 {
					summary = summary[:400]
				}
				fmt.Fprintf(&b, "  fused_summary: %s\n", summary)
			}
		}
		b.WriteByte('\n')
	}

	if len(input.RecentDialogues) > 0 {
		fmt.Fprintf(&b, "## Recent Dialogues (%d entries, for habit analysis)\n\n", len(input.RecentDialogues))
		for i, d := range input.RecentDialogues {
			if len(d) > 300 {
				d = d[:300]
			}
			fmt.Fprintf(&b, "%d. %s\n", i+1, d)
		}
		b.WriteByte('\n')
	}

	if len(input.ExistingChains) > 0 {
		fmt.Fprintf(&b, "## Existing Action Chains (%d entries, for crystal generation)\n\n", len(input.ExistingChains))
		for _, c := range input.ExistingChains {
			fmt.Fprintf(&b, "- title: %q, trigger: %q, count: %d, confidence: %.2f\n",
				c.Title, c.Trigger, c.TriggerCount, c.Confidence)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func joinStrs(primary, fallback []string) string {
	src := primary
	if len(src) == 0 {
		src = fallback
	}
	return strings.Join(src, ", ")
}

func buildTaskPrompt() string {
	var b strings.Builder
	b.WriteString("# Tasks\n\nProcess the input data for each task independently. Output everything merged into a single JSON.\n\n")
	b.WriteString(l2TaskPrompt)
	b.WriteString(l3TaskPrompt)
	b.WriteString(habitTaskPrompt)
	b.WriteString(crystalTaskPrompt)
	b.WriteString("\n# Final JSON Format\n\nMerge all tasks into:\n")
	b.WriteString(`{"l2_groups":[...], "l2_compression_needed":bool, "l1_rebuild":bool, "l0_rebuild":bool, "l3_extractions":[...], "habits":{...}, "crystals":[...]}`)
	return b.String()
}

const l2TaskPrompt = `## Task 1: L2 Topic Compression

For each scene, group related depth-1 nodes and decide if they should be merged.

Output format:
{"l2_groups": [{"scene_id": 12345, "node_hashes": [1001,1002,1003], "merged_title": "Japan Trip Planning", "merged_summary": "User planned a 7-day trip to Tokyo and Kyoto, visited temples and tried local ramen. Budget was around 200000 yen."}], "l2_compression_needed": true, "l1_rebuild": false, "l0_rebuild": false}

When no compression is needed:
{"l2_groups":[], "l2_compression_needed":false, "l1_rebuild":false, "l0_rebuild":false}
`

const l3TaskPrompt = `## Task 2: L3 Knowledge Distillation

Extract structured concepts and their relationships from each topic's keywords and summaries.

Output format:
{"l3_extractions": [{"context_id": 1001, "concepts": [{"name":"Python","type":"skill","description":"Programming language used for building backend services","keywords":["Python","backend"]}], "relations": [{"from":"Python","to":"FastAPI","kind":"Dependency"}]}]}
`

const habitTaskPrompt = `## Task 3: User Habit Analysis

Extract user traits from dialogue records.

Output format:
{"habits": {"lexicon":{"docker":"uses Docker for dev environment"},"style_traits":["prefers concise explanations"],"emotion_patterns":{"excited about new tech":"enthusiasm"}}}

Return null when no dialogue data:
{"habits": null}
`

const crystalTaskPrompt = `## Task 4: Crystal Generation

Extract reusable behavioral patterns from action chains.

Output format:
{"crystals": [{"condition":"topic:deploy","action":"run_deployment","steps":[{"action":"build_image","parameters":"{\"tag\":\"latest\"}"}],"confidence":0.85}]}

Return [] when no data:
{"crystals": []}
`

func parseConsolidatedResponse(response string) (*ConsolidationOutput, error) {
	cleaned := stripCodeBlocks(response)
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(cleaned), &root); err != nil {
		return nil, core.NewError(core.ErrSerialization, "parse JSON", err)
	}
	return &ConsolidationOutput{
		L2Groups:      parseSection[[]L2Group](root, "l2_groups"),
		L3Extractions: parseSection[[]L3Extraction](root, "l3_extractions"),
		Habits:        parseSection[HabitAnalysis](root, "habits"),
		Crystals:      parseSection[[]CrystalDef](root, "crystals"),
	}, nil
}

func parseSection[T any](root map[string]json.RawMessage, key string) Section[T] {
	raw, ok := root[key]
	if !ok || string(raw) == "null" {
		return NewEmptySection[T]()
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return NewFailedSection[T](err.Error())
	}
	return NewValidSection(v)
}
