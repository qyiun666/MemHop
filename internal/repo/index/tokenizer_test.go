// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"slices"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

func TestTokenize(t *testing.T) {
	t.Run("english", func(t *testing.T) {
		tokens := Tokenize("Hello World hello")
		assertContains(t, tokens, "hello")
		assertContains(t, tokens, "world")
	})

	t.Run("chinese", func(t *testing.T) {
		tokens := Tokenize("人工智能在医疗领域的应用")
		if len(tokens) <= 1 {
			t.Errorf("Chinese text should produce multiple tokens, got %v", tokens)
		}
	})

	t.Run("camelCase", func(t *testing.T) {
		tokens := Tokenize("fetchUserData")
		assertContains(t, tokens, "fetch")
		assertContains(t, tokens, "user")
		assertContains(t, tokens, "data")
	})

	t.Run("stop_words_filtered", func(t *testing.T) {
		tokens := Tokenize("The quick brown fox")
		assertNotContains(t, tokens, "the")
		assertContains(t, tokens, "quick")
	})

	t.Run("JSONParser", func(t *testing.T) {
		tokens := Tokenize("JSONParser")
		assertContains(t, tokens, "json")
		assertContains(t, tokens, "parser")
	})

	t.Run("getUserID", func(t *testing.T) {
		tokens := Tokenize("getUserID")
		assertContains(t, tokens, "get")
		assertContains(t, tokens, "user")
		assertContains(t, tokens, "id")
	})
}

func TestTokenizeChineseRegression(t *testing.T) {
	// CJK text must produce multiple tokens, not the whole string as one.
	tokens := Tokenize("人工智能记忆系统")
	if len(tokens) <= 1 {
		t.Errorf("CJK should split '人工智能记忆系统' into multiple tokens, got %v", tokens)
	}
}

func TestTokenizeMixedChineseEnglish(t *testing.T) {
	tokens := Tokenize("用 tokio::time::timeout 包装 async fn")
	assertContains(t, tokens, "tokio")
	assertContains(t, tokens, "timeout")
	assertContains(t, tokens, "async")
}

func TestTokenizeUnderscorePreserved(t *testing.T) {
	tokens := Tokenize("err_0x3f01")
	assertContains(t, tokens, "err")

	tokens2 := Tokenize("api_key")
	assertContains(t, tokens2, "api")
	assertContains(t, tokens2, "key")
}

func TestTokenizeAllUppercaseNotSplit(t *testing.T) {
	tokens := Tokenize("JSON")
	assertContains(t, tokens, "json")
}

func TestTokenizeChineseQuality(t *testing.T) {
	// jieba should produce "人工智能" as a single token
	tokens := Tokenize("人工智能在医疗领域的应用")
	assertContains(t, tokens, "人工智能")
	assertContains(t, tokens, "医疗")
}

func TestTokenizerEngineSelection(t *testing.T) {
	// Test that we can explicitly select gse engine
	ResetTokenizer()
	if err := InitTokenizer(EngineGse); err != nil {
		t.Fatalf("init tokenizer gse: %v", err)
	}
	tokens := Tokenize("Hello World")
	assertContains(t, tokens, "hello")
	assertContains(t, tokens, "world")

	// Reset back to default for other tests
	ResetTokenizer()
}

func TestSplitCamelCase(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"fetchUserData", []string{"fetch", "user", "data"}},
		{"JSONParser", []string{"json", "parser"}},
		{"getUserID", []string{"get", "user", "id"}},
		{"simple", []string{"simple"}},
		{"ALLCAPS", []string{"ALLCAPS"}},
		{"with_underscore", []string{"with_underscore"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := common.SplitCamelCase(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("splitCamelCase(%q) = %v, want %v", tt.input, got, tt.expected)
				return
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("splitCamelCase(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func assertContains(t *testing.T, tokens []string, expected string) {
	t.Helper()
	if slices.Contains(tokens, expected) {
		return
	}
	t.Errorf("tokens %v should contain %q", tokens, expected)
}

func assertNotContains(t *testing.T, tokens []string, unexpected string) {
	t.Helper()
	if slices.Contains(tokens, unexpected) {
		t.Errorf("tokens %v should not contain %q", tokens, unexpected)
		return
	}
}
