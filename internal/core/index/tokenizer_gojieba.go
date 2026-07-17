// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build cgo

package index

import "github.com/yanyiwu/gojieba"

// gojiebaTokenizer implements Tokenizer using gojieba (CGO binding of jieba-cpp).
type gojiebaTokenizer struct {
	jieba *gojieba.Jieba
}

func newGojiebaTokenizer() *gojiebaTokenizer {
	return &gojiebaTokenizer{jieba: gojieba.NewJieba()}
}

// Cut segments text using jieba's precise mode.
func (g *gojiebaTokenizer) Cut(text string) []string {
	return g.jieba.Cut(text, true)
}

// Close frees the native C++ jieba instance.
func (g *gojiebaTokenizer) Close() {
	if g.jieba != nil {
		g.jieba.Free()
	}
}

// createTokenizer creates the tokenizer for CGO builds.
// Prefers gojieba unless engine is explicitly "gse".
func createTokenizer(engine string) (Tokenizer, error) {
	if engine == EngineGse {
		return newGseTokenizer()
	}
	return newGojiebaTokenizer(), nil
}
