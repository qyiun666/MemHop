// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/llm"
)

// assertLLMErr fails unless err carries the LLM error code — extraction
// surfaces a bad reply as an error, never as a degraded keyword track.
func assertLLMErr(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if common.CodeOf(err) != common.ErrLLM {
		t.Fatalf("want ErrLLM, got %v (code %d)", err, common.CodeOf(err))
	}
}

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
	p := llm.New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	kw, err := llmops.ExtractKeywords(context.Background(), p, "我们聊了童年读过的书和家里的温馨时刻")
	if err != nil {
		t.Fatalf("ExtractKeywords: %v", err)
	}
	if len(kw) != 3 || kw[0] != "童年书籍" {
		t.Fatalf("want 3 keywords starting with 童年书籍, got %v", kw)
	}
}

// TestExtractKeywordsUnparseableIsError verifies that a model which never
// returns JSON fails the call: the keyword track is what a host reads back as
// its conversation context, so tokenised garbage must not be written as if the
// model had produced it.
func TestExtractKeywordsUnparseableIsError(t *testing.T) {
	srv := mockLLMServerSeq(t, "摘要", "摘要", "摘要", "还是摘要")
	p := llm.New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	kw, err := llmops.ExtractKeywords(context.Background(), p, "我们讨论了 Python 的性能优化和数据库索引")
	assertLLMErr(t, err)
	if len(kw) != 0 {
		t.Fatalf("want no keywords alongside an error, got %v", kw)
	}
}

// TestExtractKeywordsEmptyResponsesAreError verifies empty replies abort the
// caller after the retry rather than degrading.
func TestExtractKeywordsEmptyResponsesAreError(t *testing.T) {
	srv := mockLLMServerSeq(t, "", "", "", "")
	p := llm.New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	_, err := llmops.ExtractKeywords(context.Background(), p, "今天天气不错我们去爬山")
	assertLLMErr(t, err)
}

// TestExtractKeywordsBlankText verifies blank input returns empty keywords
// without any LLM call.
func TestExtractKeywordsBlankText(t *testing.T) {
	p := llm.New(&MemHopConfig{LLM: LlmConfig{APIURL: "http://127.0.0.1:1", APIKey: "test", Model: "mock"}})
	kw, err := llmops.ExtractKeywords(context.Background(), p, "   ")
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
	p := llm.New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	kw, err := llmops.ExtractKeywords(context.Background(), p, longText())
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

// TestExtractKeywordsChunkFailureIsError verifies one unparseable chunk fails
// the whole extraction: the surviving chunks would otherwise read as a complete
// keyword track while silently missing one part of the text.
func TestExtractKeywordsChunkFailureIsError(t *testing.T) {
	srv := mockLLMServerSeq(t,
		`{"keywords":["爬山"]}`,
		"这是摘要式自然语言",
		"还是摘要",
		"仍然不是 JSON",
		"格式约束重试也不是 JSON",
	)
	p := llm.New(&MemHopConfig{LLM: LlmConfig{APIURL: srv.URL, APIKey: "test", Model: "mock"}})
	_, err := llmops.ExtractKeywords(context.Background(), p, longText())
	assertLLMErr(t, err)
}

// TestExtractKeywordsTransportFailureIsError verifies a transport failure
// surfaces as itself rather than as a format failure.
func TestExtractKeywordsTransportFailureIsError(t *testing.T) {
	p := llm.New(&MemHopConfig{LLM: LlmConfig{APIURL: "http://127.0.0.1:1/v1", APIKey: "test", Model: "mock"}})
	_, err := llmops.ExtractKeywords(context.Background(), p, "随便聊点什么")
	if err == nil {
		t.Fatal("want an error from an unreachable endpoint, got nil")
	}
}
