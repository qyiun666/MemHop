// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"strings"
	"testing"
)

// TestExtractKeywordsFormatRetry verifies the format-constrained retry:
// after three non-JSON summaries exhaust the token budgets, the retry
// prompt gets a valid JSON reply.
func TestExtractKeywordsFormatRetry(t *testing.T) {
	srv := mockLLMServerSeq(t,
		"这段对话温馨地展现了通过分享童年书籍和家庭时刻",
		"这段对话温馨地展现了通过分享童年书籍和家庭时刻",
		"这段对话温馨地展现了通过分享童年书籍和家庭时刻",
		`{"keywords":["童年书籍","家庭时刻","分享"]}`,
	)
	p := New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	kw, err := p.ExtractKeywords(context.Background(), "我们聊了童年读过的书和家里的温馨时刻")
	if err != nil {
		t.Fatalf("ExtractKeywords: %v", err)
	}
	if len(kw) != 3 || kw[0] != "童年书籍" {
		t.Fatalf("want 3 keywords starting with 童年书籍, got %v", kw)
	}
}

// TestExtractKeywordsHeuristicFallback verifies degradation: every reply
// is natural-language prose, so extraction falls back to heuristic
// tokenization instead of returning an error.
func TestExtractKeywordsHeuristicFallback(t *testing.T) {
	srv := mockLLMServerSeq(t, "摘要", "摘要", "摘要", "还是摘要")
	p := New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	kw, err := p.ExtractKeywords(context.Background(), "我们讨论了 Python 的性能优化和数据库索引")
	if err != nil {
		t.Fatalf("ExtractKeywords should degrade, got error: %v", err)
	}
	if len(kw) == 0 {
		t.Fatal("want non-empty heuristic keywords")
	}
}

// TestExtractKeywordsEmptyResponsesDegrade verifies empty replies never
// abort the caller: "" → format retry → heuristic fallback.
func TestExtractKeywordsEmptyResponsesDegrade(t *testing.T) {
	srv := mockLLMServerSeq(t, "", "", "", "")
	p := New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	kw, err := p.ExtractKeywords(context.Background(), "今天天气不错我们去爬山")
	if err != nil {
		t.Fatalf("ExtractKeywords should degrade, got error: %v", err)
	}
	if len(kw) == 0 {
		t.Fatal("want non-empty heuristic keywords")
	}
}

// TestExtractKeywordsBlankText verifies blank input returns empty keywords
// without any LLM call.
func TestExtractKeywordsBlankText(t *testing.T) {
	p := New(&MemHopConfig{LLM: LlmConfig{APIURL: "http://127.0.0.1:1", APIKey: "test", Model: "mock"}})
	kw, err := p.ExtractKeywords(context.Background(), "   ")
	if err != nil {
		t.Fatalf("ExtractKeywords: %v", err)
	}
	if len(kw) != 0 {
		t.Fatalf("want empty keywords, got %v", kw)
	}
}

// longText builds a >keywordChunkRunes input so extraction chunks it.
func longText() string {
	return strings.Repeat("今天天气不错我们去爬山看日出。", 300)
}

// TestExtractKeywordsChunkedMerge verifies long inputs are chunked and
// per-chunk keywords are merged in order.
func TestExtractKeywordsChunkedMerge(t *testing.T) {
	srv := mockLLMServerSeq(t,
		`{"keywords":["爬山"]}`,
		`{"keywords":["日出"]}`,
		`{"keywords":["好天气"]}`,
	)
	p := New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	kw, err := p.ExtractKeywords(context.Background(), longText())
	if err != nil {
		t.Fatalf("ExtractKeywords: %v", err)
	}
	want := []string{"爬山", "日出", "好天气"}
	if len(kw) != len(want) {
		t.Fatalf("want %v, got %v", want, kw)
	}
	for i := range want {
		if kw[i] != want[i] {
			t.Fatalf("want %v, got %v", want, kw)
		}
	}
}

// TestExtractKeywordsChunkedPartialFailure verifies unparseable chunks are
// dropped while the rest still merge, without an error.
func TestExtractKeywordsChunkedPartialFailure(t *testing.T) {
	srv := mockLLMServerSeq(t,
		`{"keywords":["爬山"]}`,
		"这是摘要式自然语言",
		`{"keywords":["好天气"]}`,
	)
	p := New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	kw, err := p.ExtractKeywords(context.Background(), longText())
	if err != nil {
		t.Fatalf("ExtractKeywords should degrade, got error: %v", err)
	}
	if len(kw) != 2 || kw[0] != "爬山" || kw[1] != "好天气" {
		t.Fatalf("want [爬山 好天气], got %v", kw)
	}
}

// TestExtractKeywordsChunkedAllFail verifies every chunk failing still
// degrades to heuristic keywords instead of an error.
func TestExtractKeywordsChunkedAllFail(t *testing.T) {
	srv := mockLLMServerSeq(t, "摘要一", "摘要二", "摘要三")
	p := New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	kw, err := p.ExtractKeywords(context.Background(), longText())
	if err != nil {
		t.Fatalf("ExtractKeywords should degrade, got error: %v", err)
	}
	if len(kw) == 0 {
		t.Fatal("want non-empty heuristic keywords")
	}
}
