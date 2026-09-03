// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

// tuning.go hosts the composition root's remaining assembly constant: the
// tokenizer engine selector of Open. The consolidation tuning (L1 decay
// parameters, hyperedge similarity floor, usage-feedback window) moved with
// the stages to internal/dream.

const (
	defaultTokenizerEngine string = "auto"
)
