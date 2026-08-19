// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// llm_client.go 封装 LLM 调用客户端：一个 Provider 供三个调用点共享
// （语义关键词提取、L2 压缩、L1→L0 蒸馏）。只负责
// 参数进来 → 构建 prompt → 调 LLM → 解析 → 返回结果。

package internal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	openai "github.com/sashabaranov/go-openai"
)

const (
	// defaultTimeoutSecs 是 Config.TimeoutSecs 未设置时的 HTTP 超时。
	defaultTimeoutSecs = 120
	// defaultMaxOutputTokens 是 Config.MaxOutputTokens 未设置时的输出上限。
	defaultMaxOutputTokens = 8192
)

// Provider 是 go-openai 客户端的薄封装，供三个调用点共享。
type Provider struct {
	client          *openai.Client
	model           string
	maxOutputTokens int
}

// New 从配置创建 Provider。
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

// normalizeBaseURL 确保 BaseURL 以 /v1 结尾（go-openai 不自动补），
// 并剥离可能传入的完整 /chat/completions 后缀（SDK 会重新拼接）。
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

// chat 执行一次非流式 chat completion，对 429/5xx 做指数退避重试
// （500ms → 2s，共 3 次尝试），其余错误不重试。
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

// httpError 从 go-openai 错误中提取 HTTP 状态码与消息体；
// RequestError 优先（go-openai 可能在其中包裹零值 APIError），
// 非 HTTP 错误返回 (0, "")。
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

// retryable 判断状态码是否属于值得重试的瞬时错误（429 与 5xx）。
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

// stripCodeBlocks 去掉 LLM 输出中 ```lang ... ``` markdown 围栏。
func stripCodeBlocks(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	body := trimmed[3:]
	if nl := strings.IndexByte(body, '\n'); nl >= 0 {
		body = body[nl+1:]
	} else {
		// 只有围栏没有正文（如 "```json```"），视为空内容。
		body = ""
	}
	if end := strings.LastIndex(body, "```"); end >= 0 {
		body = body[:end]
	}
	return strings.TrimSpace(body)
}
