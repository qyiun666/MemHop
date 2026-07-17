// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"fmt"

	"memhop/internal/query/dream"
	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
)

// DreamOptions holds optional parameters for the Dream call.
// Both fields are optional: nil/empty means use default.
type DreamOptions struct {
	LLM   dream.LlmProvider // nil = use LLM configured in MemHopConfig (default)
	L2IDs []string          // nil/empty = consolidate all L2 topics (default)
}

// Dream runs the memory consolidation pipeline.
// opts may be nil for all-default behavior (use config LLM, process all topics).
func (m *MemHop) Dream(opts *DreamOptions) (*dream.DreamReport, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}

	var llm dream.LlmProvider
	var l2IDs []string
	if opts != nil {
		llm = opts.LLM
		l2IDs = opts.L2IDs
	}
	if llm == nil {
		llm = dream.NewOpenAIProvider(&m.config.LLM)
	}
	uint64IDs, err := parseL2IDs(l2IDs)
	if err != nil {
		return nil, err
	}

	result, err := dream.RunPipeline(
		m.engine, m.sparseIndex, llm,
		uint64IDs, m.defaults.DecayConfig,
		m.l2Meta, m.encoder,
	)
	if err != nil {
		return nil, err
	}

	m.l2Meta = result.L2Meta
	m.l1Reverse = result.L1Reverse
	return result.Report, nil
}

func parseL2IDs(ids []string) ([]uint64, error) {
	out := make([]uint64, 0, len(ids))
	var invalid []string
	for _, id := range ids {
		h, err := hash.ParseID(id)
		if err != nil {
			invalid = append(invalid, id)
			continue
		}
		out = append(out, h)
	}
	if len(invalid) > 0 {
		return out, fmt.Errorf("memhop: invalid L2 IDs: %v", invalid)
	}
	return out, nil
}
