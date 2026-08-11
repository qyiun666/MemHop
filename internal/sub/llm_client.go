// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// llm_client.go wraps the LLM client: one Provider shared by the three
// call points (keyword extraction, L2 consolidation, L1→L0 distillation).

package sub

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/sub/common"
	openai "github.com/sashabaranov/go-openai"
)

const (
	// defaultTimeoutSecs: HTTP timeout when Config.TimeoutSecs is unset.
	defaultTimeoutSecs = 120
	// defaultMaxOutputTokens: output cap when Config.MaxOutputTokens is unset.
	defaultMaxOutputTokens = 8192
)

type Provider struct {
	client          *openai.Client
	model           string
	maxOutputTokens int
}

func New(cfg *MemHopConfig) *Provider {
	timeoutSecs := cfg.LLM.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = defaultTimeoutSecs
	}
	maxTokens := cfg.LLM.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxOutputTokens
	}
	oc := openai.DefaultConfig(cfg.LLM.APIKey)
	oc.BaseURL = normalizeBaseURL(cfg.LLM.APIURL)
	oc.HTTPClient = &http.Client{Timeout: time.Duration(timeoutSecs) * time.Second}
	return &Provider{
		client:          openai.NewClientWithConfig(oc),
		model:           cfg.LLM.Model,
		maxOutputTokens: maxTokens,
	}
}

// normalizeBaseURL ensures a /v1 suffix (go-openai does not append it)
// and strips a full /chat/completions suffix.
func normalizeBaseURL(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	if strings.HasSuffix(u, "/chat/completions") {
		u = strings.TrimSuffix(u, "/chat/completions")
	}
	if !strings.HasSuffix(u, "/v1") {
		u += "/v1"
	}
	return u
}

// chat runs one non-streaming completion with exponential backoff on
// 429/5xx (500ms → 2s, 3 attempts); other errors are not retried.
func (p *Provider) chat(
	ctx context.Context, system, user string, maxTokens int, temperature, topP float32,
) (string, error) {
	req := openai.ChatCompletionRequest{
		Model: p.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: system},
			{Role: openai.ChatMessageRoleUser, Content: user},
		},
		MaxTokens:        maxTokens,
		Temperature:      temperature,
		TopP:             topP,
		PresencePenalty:  0.0,
		FrequencyPenalty: 0.0,
		Stream:           false,
	}
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
		resp, err := p.client.CreateChatCompletion(ctx, req)
		if err != nil {
			status, msg := httpError(err)
			if status > 0 {
				lastErr = common.NewError(common.ErrLLM, fmt.Sprintf("llm api: %d - %s", status, msg))
				if !retryable(status) || attempt == len(delays) {
					return "", lastErr
				}
				continue
			}
			return "", common.NewError(common.ErrLLM, "llm api call failed", err)
		}
		if len(resp.Choices) == 0 {
			return "", common.NewError(common.ErrLLM, "llm response has no choices")
		}
		return resp.Choices[0].Message.Content, nil
	}
	return "", lastErr
}

// httpError extracts the HTTP status and body from go-openai errors;
// non-HTTP errors return (0, "").
func httpError(err error) (int, string) {
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) && reqErr.HTTPStatusCode > 0 {
		return reqErr.HTTPStatusCode, string(reqErr.Body)
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatusCode > 0 {
		return apiErr.HTTPStatusCode, apiErr.Message
	}
	return 0, ""
}

func retryable(status int) bool {
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

// stripCodeBlocks removes ```lang ... ``` markdown fences from LLM output.
func stripCodeBlocks(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	body := trimmed[3:]
	if nl := strings.IndexByte(body, '\n'); nl >= 0 {
		body = body[nl+1:]
	} else {
		// fence without body (e.g. "```json```") counts as empty
		body = ""
	}
	if end := strings.LastIndex(body, "```"); end >= 0 {
		body = body[:end]
	}
	return strings.TrimSpace(body)
}
