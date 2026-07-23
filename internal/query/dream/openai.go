// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"memhop/internal/common/config"
	"memhop/internal/common/mherrors"
	"memhop/internal/common/strutil"
)

const (
	// defaultTimeoutSecs is the fallback HTTP timeout when LlmConfig.TimeoutSecs is unset.
	defaultTimeoutSecs = 120
	// defaultMaxOutputTokens covers DeepSeek-chat / Qwen / Claude Haiku output ceilings.
	defaultMaxOutputTokens = 8192
	// chatCompletionsSuffix is the OpenAI-compatible chat completions path.
	chatCompletionsSuffix = "/v1/chat/completions"
)

const systemConsolidate = `You are a JSON extraction engine for memory consolidation. Analyze the chat memories and output structured JSON.

Rules:
- Output ONLY valid JSON, no markdown, no code fences, no extra text
- Each field can independently be null (set to null, don't omit)
- Preserve all proper nouns, technical terms, version numbers, and numbers
- Keep mixed-language terms (English+Chinese) in their original form
- Extract concepts based solely on facts in the input, never speculate`

// OpenAIProvider is an OpenAI-compatible LLM provider that satisfies both
// LlmProvider (Consolidate) and ChatProvider (Chat) interfaces.
//
// Security / trust boundary: cfg.APIURL is treated as trusted host-supplied
// configuration. MemHop is an embedded library — the embedding application
// (e.g. MeowAgent) is responsible for validating the URL if it originates
// from external / user-controlled input. Loopback and private-network URLs
// are intentionally allowed to support local Ollama and self-hosted LLM
// endpoints, so no SSRF allowlist is enforced at this layer.
type OpenAIProvider struct {
	apiURL          string
	apiKey          string
	model           string
	maxOutputTokens int
	timeout         time.Duration
	httpClient      *http.Client
}

// NewOpenAIProvider creates an OpenAI provider from config.
// The APIURL is normalized at construction so callers may pass any of:
// base URL (https://api.deepseek.com), API root (https://api.openai.com/v1),
// or the full chat completions endpoint — all three resolve to /v1/chat/completions.
func NewOpenAIProvider(cfg *config.LlmConfig) *OpenAIProvider {
	timeoutSecs := cfg.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = defaultTimeoutSecs
	}
	maxTokens := cfg.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxOutputTokens
	}
	timeout := time.Duration(timeoutSecs) * time.Second
	return &OpenAIProvider{
		apiURL:          normalizeChatURL(cfg.APIURL),
		apiKey:          cfg.APIKey,
		model:           cfg.Model,
		maxOutputTokens: maxTokens,
		timeout:         timeout,
		httpClient:      &http.Client{Timeout: timeout},
	}
}

// normalizeChatURL accepts a base URL, /v1 root, or full chat completions
// URL and returns the full /v1/chat/completions endpoint. Trailing slashes
// are stripped before matching so both "https://x.y/" and "https://x.y"
// resolve identically.
func normalizeChatURL(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	switch {
	case strings.HasSuffix(u, chatCompletionsSuffix):
		return u
	case strings.HasSuffix(u, "/v1"):
		return u + "/chat/completions"
	default:
		return u + chatCompletionsSuffix
	}
}

// chatParams collects the tunable knobs for a single chat completion call.
type chatParams struct {
	MaxTokens   int
	Temperature float32
	TopP        float32
	// ErrPrefix keeps historical log formats stable ("chat api: …" vs "api request failed: …").
	ErrPrefix string
}

// Chat sends a chat completion request with custom parameters.
// The request runs on context.Background(); use ChatWithContext for cancellation.
func (p *OpenAIProvider) Chat(system, user string, maxTokens int, temperature, topP float32) (string, error) {
	return p.ChatWithContext(context.Background(), system, user, maxTokens, temperature, topP)
}

// ChatWithContext is Chat with an explicit context for cancellation / deadline propagation.
func (p *OpenAIProvider) ChatWithContext(
	ctx context.Context, system, user string, maxTokens int, temperature, topP float32,
) (string, error) {
	return p.do(ctx, system, user, chatParams{
		MaxTokens: maxTokens, Temperature: temperature, TopP: topP,
		ErrPrefix: "chat api",
	})
}

// Consolidate performs the LLM consolidation call.
func (p *OpenAIProvider) Consolidate(input *ConsolidationInput) (*ConsolidationOutput, error) {
	return p.ConsolidateWithContext(context.Background(), input)
}

// ConsolidateWithContext runs Consolidate with an explicit context.
func (p *OpenAIProvider) ConsolidateWithContext(
	ctx context.Context, input *ConsolidationInput,
) (*ConsolidationOutput, error) {
	data := buildDataSection(input)
	tasks := buildTaskPrompt()
	// Tasks before data: the model needs to know WHAT to extract before reading the data.
	userPrompt := tasks + "\n\n# Input Data\n\n" + data

	response, err := p.callWithRetry(ctx, systemConsolidate, userPrompt)
	if err != nil {
		return nil, err
	}
	return parseConsolidatedResponse(response)
}

// callAPI runs a single consolidation chat call at temperature=0 / top_p=1
// with the configured max_tokens.
func (p *OpenAIProvider) callAPI(ctx context.Context, system, user string) (string, error) {
	return p.do(ctx, system, user, chatParams{
		MaxTokens: p.maxOutputTokens, Temperature: 0.0, TopP: 1.0,
		ErrPrefix: "api request",
	})
}

// do is the single HTTP dispatch path used by every chat / consolidate call.
// It transparently retries 429 and 5xx (500/502/503/504) responses with
// exponential backoff (500ms -> 2s), honoring ctx.Done().
func (p *OpenAIProvider) do(
	ctx context.Context, system, user string, params chatParams,
) (string, error) {
	body := map[string]interface{}{
		"model": p.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"max_tokens":        params.MaxTokens,
		"temperature":       params.Temperature,
		"top_p":             params.TopP,
		"presence_penalty":  0.0,
		"frequency_penalty": 0.0,
		"stream":            false,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", mherrors.NewError(mherrors.ErrLLM, "marshal "+params.ErrPrefix+" request", err)
	}
	// Backoff schedule: len(delays)+1 total attempts; delays[i] is the sleep BEFORE attempt i+1.
	delays := []time.Duration{500 * time.Millisecond, 2 * time.Second}
	var lastErr error
	for attempt := 0; attempt <= len(delays); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delays[attempt-1]):
			}
		}
		status, respBody, err := p.attempt(ctx, bodyBytes, params.ErrPrefix)
		if err != nil {
			// Network / marshal / request-build error: don't retry.
			return "", err
		}
		if status == http.StatusOK {
			return decodeChatContent(respBody, params.ErrPrefix)
		}
		httpErr := mherrors.NewError(mherrors.ErrLLM,
			fmt.Sprintf("%s: %d - %s", params.ErrPrefix, status, string(respBody)))
		if !isRetryableStatus(status) || attempt == len(delays) {
			return "", httpErr
		}
		lastErr = httpErr
	}
	return "", lastErr
}

// attempt sends one HTTP request and returns the status code plus the read body.
// The response body is fully drained and closed before returning.
func (p *OpenAIProvider) attempt(
	ctx context.Context, bodyBytes []byte, errPrefix string,
) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return 0, nil, mherrors.NewError(mherrors.ErrLLM, "create "+errPrefix+" request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return 0, nil, mherrors.NewError(mherrors.ErrLLM, errPrefix+" call failed", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body, nil
}

// isRetryableStatus returns true for status codes that indicate a transient
// server-side or rate-limit condition worth retrying.
func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// decodeChatContent extracts the first choice's message.content from a
// non-streaming chat completions response body.
func decodeChatContent(body []byte, errPrefix string) (string, error) {
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", mherrors.NewError(mherrors.ErrSerialization, "parse "+errPrefix+" response", err)
	}
	if len(result.Choices) == 0 {
		return "", mherrors.NewError(mherrors.ErrSerialization, "no content in "+errPrefix+" response")
	}
	return result.Choices[0].Message.Content, nil
}

// callWithRetry invokes callAPI once, and on JSON parse failure retries once
// with a stricter instruction appended. The retry response is re-validated
// against parseConsolidatedResponse so callers can distinguish "unrecoverable
// retry" from generic parse errors.
func (p *OpenAIProvider) callWithRetry(ctx context.Context, system, user string) (string, error) {
	response, err := p.callAPI(ctx, system, user)
	if err != nil {
		return "", err
	}
	if _, parseErr := parseConsolidatedResponse(response); parseErr == nil {
		return response, nil
	}
	retryPrompt := user + "\n\n" + jsonRetryHint
	retryResp, err := p.callAPI(ctx, system, retryPrompt)
	if err != nil {
		return "", err
	}
	if _, parseErr := parseConsolidatedResponse(retryResp); parseErr != nil {
		return "", mherrors.NewError(mherrors.ErrSerialization,
			"consolidation retry response still unparseable", parseErr)
	}
	return retryResp, nil
}

const jsonRetryHint = "Your previous response could not be parsed. Output ONLY valid JSON with no markdown code blocks or extra text."

// stripCodeBlocks strips optional ```lang ... ``` markdown fences from LLM output.
// Degenerate single-line fences without a body (e.g. "```json```") return "".
func stripCodeBlocks(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	stripped := trimmed[3:]
	start := strings.IndexByte(stripped, '\n')
	if start >= 0 {
		stripped = stripped[start+1:]
	} else {
		// No newline after the opening fence: the remainder is only a language
		// tag (or nothing). Treat as empty body so the downstream parser reports
		// a clean "empty JSON" error rather than trying to parse "json".
		stripped = ""
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
				// Rune-safe truncation preserves CJK boundaries so the prompt
				// never contains a partial multi-byte sequence.
				summary := strutil.SafeCharSlice(*node.FusedSummary, 400)
				fmt.Fprintf(&b, "  fused_summary: %s\n", summary)
			}
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
	b.WriteString("\n# Final JSON Format\n\nMerge all tasks into:\n")
	b.WriteString(`{"l2_groups":[...], "l2_compression_needed":bool}`)
	return b.String()
}

const l2TaskPrompt = `## Task 1: L2 Topic Compression

For each scene, group related depth-1 nodes and decide if they should be merged.

Output format:
{"l2_groups": [{"scene_id": 12345, "node_hashes": [1001,1002,1003], "merged_title": "Japan Trip Planning", "merged_summary": "User planned a 7-day trip to Tokyo and Kyoto, visited temples and tried local ramen. Budget was around 200000 yen."}], "l2_compression_needed": true}

When no compression is needed:
{"l2_groups":[], "l2_compression_needed":false}
`

func parseConsolidatedResponse(response string) (*ConsolidationOutput, error) {
	cleaned := stripCodeBlocks(response)
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(cleaned), &root); err != nil {
		return nil, mherrors.NewError(mherrors.ErrSerialization, "parse JSON", err)
	}
	return &ConsolidationOutput{
		L2Groups: parseSection[[]L2Group](root, "l2_groups"),
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
