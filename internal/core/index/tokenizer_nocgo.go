// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build !cgo

package index

// createTokenizer creates the tokenizer for non-CGO builds.
// Only gse is available; engine parameter is ignored.
func createTokenizer(engine string) (Tokenizer, error) {
	return newGseTokenizer()
}
