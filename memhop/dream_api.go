// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"fmt"
	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/dream"
	"github.com/qyiun666/memhop/memhop/internal/core/index"
	"github.com/qyiun666/memhop/memhop/internal/core/query"
	"github.com/qyiun666/memhop/memhop/internal/hash"
)

// DreamOptions holds optional parameters for the Dream call.
// Both fields are optional: nil/empty means use default.
type DreamOptions struct {
	LLM   dream.LlmProvider // nil = use LLM configured in MemHopConfig (default)
	L2IDs []string          // nil/empty = consolidate all L2 topics (default)
}

// Dream runs the memory consolidation pipeline.
// opts may be nil for all-default behavior (use config LLM, process all topics).
// Holds write lock for the entire duration to protect shared index mutations.
func (m *MemHop) Dream(opts *DreamOptions) (*dream.DreamReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, core.ErrClosed
	}

	// Extract optional fields.
	var llm dream.LlmProvider
	var l2IDs []string
	if opts != nil {
		llm = opts.LLM
		l2IDs = opts.L2IDs
	}

	// Fallback: use config LLM if caller did not provide one.
	if llm == nil {
		llm = dream.NewOpenAIProvider(&m.config.LLM)
	}

	uint64IDs, err := parseL2IDs(l2IDs)
	if err != nil {
		return nil, err
	}

	// Run dream pipeline under write lock (mutates SparseIndex/L2MetaIndex).
	report, err := dream.DreamPipeline(
		m.engine, m.sparseIndex, llm,
		uint64IDs, m.defaults.DecayConfig,
		m.l2Meta, m.encoder,
	)
	if err != nil {
		return nil, err
	}

	// Rebuild indexes from updated engine state.
	m.l2Meta = index.BuildL2MetaFromEngine(m.engine)
	m.l1Reverse = query.BuildL1ReverseIndex(m.engine)

	return report, nil
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
