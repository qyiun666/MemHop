// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"memhop/internal/common/config"
)

// TestNormalizeChatURL covers the pure normalization logic — no HTTP.
func TestNormalizeChatURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"base_url", "https://api.deepseek.com", "https://api.deepseek.com/v1/chat/completions"},
		{"base_url_trailing_slash", "https://api.deepseek.com/", "https://api.deepseek.com/v1/chat/completions"},
		{"v1_root", "https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"},
		{"v1_root_trailing_slash", "https://api.openai.com/v1/", "https://api.openai.com/v1/chat/completions"},
		{"full_url", "https://api.deepseek.com/v1/chat/completions", "https://api.deepseek.com/v1/chat/completions"},
		{"full_url_trailing_slash", "https://api.deepseek.com/v1/chat/completions/", "https://api.deepseek.com/v1/chat/completions"},
		{"whitespace_padded", "  https://api.deepseek.com  ", "https://api.deepseek.com/v1/chat/completions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeChatURL(tc.in)
			if got != tc.want {
				t.Fatalf("normalizeChatURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestOpenAIProvider_ChatURLForms verifies that Chat() reaches
// /v1/chat/completions regardless of which URL form the caller provided.
func TestOpenAIProvider_ChatURLForms(t *testing.T) {
	var seenPath atomic.Value
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath.Store(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	forms := []string{
		srv.URL,                           // base URL
		srv.URL + "/",                     // trailing slash
		srv.URL + "/v1",                   // API root
		srv.URL + "/v1/chat/completions",  // full URL
		srv.URL + "/v1/chat/completions/", // full URL + trailing slash
	}
	for _, form := range forms {
		t.Run(form, func(t *testing.T) {
			p := NewOpenAIProvider(&config.LlmConfig{
				APIURL: form, APIKey: "sk-test", Model: "test-model",
				TimeoutSecs: 5,
			})
			resp, err := p.Chat("sys", "usr", 128, 0.0, 1.0)
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if resp != "ok" {
				t.Fatalf("Chat resp = %q, want %q", resp, "ok")
			}
			if got := seenPath.Load(); got != "/v1/chat/completions" {
				t.Fatalf("server saw path %q, want /v1/chat/completions", got)
			}
		})
	}
}

// TestOpenAIProvider_MaxTokensPayload asserts the configured MaxOutputTokens
// value is what actually reaches the wire (T2 regression guard: previously
// hardcoded to 128000).
func TestOpenAIProvider_MaxTokensPayload(t *testing.T) {
	var seenMaxTokens atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]interface{}
		_ = json.Unmarshal(body, &payload)
		if v, ok := payload["max_tokens"].(float64); ok {
			seenMaxTokens.Store(int64(v))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"l2_groups\":[]}"}}]}`)
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	t.Run("default_8192", func(t *testing.T) {
		seenMaxTokens.Store(0)
		p := NewOpenAIProvider(&config.LlmConfig{
			APIURL: srv.URL, APIKey: "sk-test", Model: "m", TimeoutSecs: 5,
		})
		if _, err := p.Consolidate(&ConsolidationInput{}); err != nil {
			t.Fatalf("Consolidate: %v", err)
		}
		if got := seenMaxTokens.Load(); got != 8192 {
			t.Fatalf("default max_tokens = %d, want 8192", got)
		}
	})

	t.Run("configured_4096", func(t *testing.T) {
		seenMaxTokens.Store(0)
		p := NewOpenAIProvider(&config.LlmConfig{
			APIURL: srv.URL, APIKey: "sk-test", Model: "m",
			TimeoutSecs: 5, MaxOutputTokens: 4096,
		})
		if _, err := p.Consolidate(&ConsolidationInput{}); err != nil {
			t.Fatalf("Consolidate: %v", err)
		}
		if got := seenMaxTokens.Load(); got != 4096 {
			t.Fatalf("configured max_tokens = %d, want 4096", got)
		}
	})
}

// TestOpenAIProvider_RetryOn5xx confirms exponential backoff kicks in for
// 429 / 5xx responses and eventually succeeds when the server recovers.
func TestOpenAIProvider_RetryOn5xx(t *testing.T) {
	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Fresh provider with tiny timeout so a broken retry loop fails fast.
	p := NewOpenAIProvider(&config.LlmConfig{
		APIURL: srv.URL, APIKey: "sk-test", Model: "m", TimeoutSecs: 10,
	})
	start := time.Now()
	resp, err := p.Chat("sys", "usr", 128, 0.0, 1.0)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp != "ok" {
		t.Fatalf("Chat resp = %q", resp)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("total server hits = %d, want 3", got)
	}
	// First delay 500ms, second delay 2s; total must be >= 2.4s.
	if elapsed < 2400*time.Millisecond {
		t.Fatalf("elapsed %v suggests no backoff", elapsed)
	}
}

// TestOpenAIProvider_ContextCancel ensures ChatWithContext honors an
// already-canceled context — the request must fail before hitting the wire.
func TestOpenAIProvider_ContextCancel(t *testing.T) {
	// Point at an unreachable port; the pre-canceled ctx must short-circuit
	// http.NewRequestWithContext before any network I/O occurs.
	p := NewOpenAIProvider(&config.LlmConfig{
		APIURL: "http://127.0.0.1:1", APIKey: "sk-test", Model: "m", TimeoutSecs: 30,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	start := time.Now()
	_, err := p.ChatWithContext(ctx, "sys", "usr", 128, 0.0, 1.0)
	if err == nil {
		t.Fatal("Chat returned nil error after ctx cancel")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("ctx-canceled call blocked for %v; should short-circuit", time.Since(start))
	}
}

// TestStripCodeBlocks covers edge cases including the single-line fence.
func TestStripCodeBlocks(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain_json", `{"a":1}`, `{"a":1}`},
		{"fenced_json", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"fenced_no_lang", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"single_line_lang_tag", "```json```", ""},
		{"unterminated_fence", "```json\n{\"a\":1}", `{"a":1}`},
		{"whitespace_padded", "  \n```\n{}\n```  \n", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripCodeBlocks(tc.in)
			if got != tc.want {
				t.Fatalf("stripCodeBlocks(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestOpenAIProvider_ErrorSurface verifies that non-retryable client-side
// errors (4xx except 429) do NOT retry and surface with a stable prefix.
func TestOpenAIProvider_ErrorSurface(t *testing.T) {
	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"nope"}`)
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	p := NewOpenAIProvider(&config.LlmConfig{
		APIURL: srv.URL, APIKey: "sk", Model: "m", TimeoutSecs: 5,
	})
	_, err := p.Chat("sys", "usr", 128, 0.0, 1.0)
	if err == nil {
		t.Fatal("expected error on 400 response")
	}
	if !strings.Contains(err.Error(), "chat api: 400") {
		t.Fatalf("error missing stable prefix: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("total server hits on 400 = %d, want 1 (no retry)", got)
	}
}
