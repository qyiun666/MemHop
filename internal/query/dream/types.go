// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Dream-level DTOs for the MemHop Query layer.

package dream

// L3EntityHint is a hint for L3 knowledge graph entity import.
type L3EntityHint struct {
	Name       string `json:"name"`
	EntityType string `json:"type"`
}

// SearchPreprocessResult is the result of LLM search query preprocessing.
type SearchPreprocessResult struct {
	Keywords      []string       `json:"keywords"`
	NeedsL3Import bool           `json:"needs_l3_import"`
	L3Entities    []L3EntityHint `json:"l3_entities,omitempty"`
}
