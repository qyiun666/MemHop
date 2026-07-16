// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"fmt"

	"github.com/go-ego/gse"
)

// gseTokenizer implements Tokenizer using the gse library (pure Go).
type gseTokenizer struct {
	seg *gse.Segmenter
}

func newGseTokenizer() (*gseTokenizer, error) {
	s, err := gse.New()
	if err != nil {
		return nil, fmt.Errorf("gse tokenizer init failed: %w", err)
	}
	return &gseTokenizer{seg: &s}, nil
}

// Cut segments text using gse's precise mode.
func (g *gseTokenizer) Cut(text string) []string {
	return g.seg.Cut(text, true)
}

// Close is a no-op for gse (no native resources to release).
func (g *gseTokenizer) Close() {}
