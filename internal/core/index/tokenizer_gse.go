// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"fmt"

	"github.com/go-ego/gse"
)

// gseTokenizer implements Tokenizer using the pure-Go gse library.
// It loads the embedded "zh_s" dictionary (~350k Simplified-Chinese
// tokens) at construction so BM25 recall stays strong without CGO or
// external dictionary files.
type gseTokenizer struct {
	seg *gse.Segmenter
}

func newGseTokenizer() (*gseTokenizer, error) {
	s, err := gse.NewEmbed("zh_s")
	if err != nil {
		return nil, fmt.Errorf("gse tokenizer init failed: %w", err)
	}
	return &gseTokenizer{seg: &s}, nil
}

// Cut segments text in precise mode with HMM enabled so out-of-vocabulary
// CJK terms are still recognised.
func (g *gseTokenizer) Cut(text string) []string {
	return g.seg.Cut(text, true)
}

// Close is a no-op for gse (no native resources to release).
func (g *gseTokenizer) Close() {}

// createTokenizer builds the global tokenizer. gse is the only backend;
// the engine parameter is kept for API compatibility.
func createTokenizer(engine string) (Tokenizer, error) {
	return newGseTokenizer()
}
