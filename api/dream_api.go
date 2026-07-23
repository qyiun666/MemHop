// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"context"
	"fmt"

	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
	"memhop/internal/query/dream"
)

// DreamOptions holds optional parameters for the Dream call.
// All fields are optional; a nil *DreamOptions or zero value produces
// historical default behavior.
//
// Runtime cost: each Dream invocation performs at most three outbound LLM
// calls — one Consolidate, at most one Consolidate retry on JSON parse
// failure, and one L0 distill Chat. Setting SkipDistill=true caps the total
// at two.
type DreamOptions struct {
	// LLM overrides the default consolidation provider. nil = build one from
	// MemHopConfig.LLM. Custom providers are used verbatim for Consolidate.
	LLM dream.LlmProvider

	// Chat overrides the default chat provider used by the L0 distill stage.
	// nil = fall back to opts.LLM when it also satisfies ChatProvider
	// (OpenAIProvider does), otherwise build one from MemHopConfig.LLM.
	Chat dream.ChatProvider

	// L2IDs restricts consolidation to the listed L2 topics.
	// nil / empty = process all L2 topics.
	// Any invalid hex ID causes the whole call to fail-fast (strict semantics).
	L2IDs []string

	// SkipDistill disables the L0 emotion/MBTI distill stage. The resulting
	// DreamReport.Stages still contains an l0_distill entry with Status="skipped"
	// so downstream consumers can rely on Stages length == 5.
	SkipDistill bool
}

// Dream runs the memory consolidation pipeline.
// opts may be nil for all-default behavior (use config LLM, process all topics).
func (m *MemHop) Dream(opts *DreamOptions) (*dream.DreamReport, error) {
	return m.DreamWithContext(context.Background(), opts)
}

// DreamWithContext is Dream with an explicit context. Currently the context
// is threaded through into the LLM HTTP client when a custom opts.LLM or
// opts.Chat also honors it (OpenAIProvider does via ChatWithContext /
// ConsolidateWithContext). The pipeline itself is synchronous and does not
// poll ctx.Done() between stages; use context cancellation for LLM timeouts.
func (m *MemHop) DreamWithContext(_ context.Context, opts *DreamOptions) (*dream.DreamReport, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}

	llm, chat, l2IDs, skipDistill := m.resolveDreamDeps(opts)
	uint64IDs, err := parseL2IDs(l2IDs)
	if err != nil {
		return nil, err
	}

	result, err := dream.RunPipeline(
		m.engine, m.sparseIndex, llm, chat,
		uint64IDs, m.defaults.DecayConfig,
		m.getL2Meta(), m.encoder,
		dream.RunOptions{SkipDistill: skipDistill},
	)
	if err != nil {
		return nil, err
	}

	m.l2Meta.Store(result.L2Meta)
	m.l1Reverse.Store(result.L1Reverse)
	return result.Report, nil
}

// resolveDreamDeps applies option defaults, reusing the LLM as chat when
// possible to honor the DreamOptions.LLM contract that a caller-injected
// provider should route ALL LLM calls in the pipeline.
func (m *MemHop) resolveDreamDeps(
	opts *DreamOptions,
) (dream.LlmProvider, dream.ChatProvider, []string, bool) {
	var (
		llm         dream.LlmProvider
		chat        dream.ChatProvider
		l2IDs       []string
		skipDistill bool
	)
	if opts != nil {
		llm = opts.LLM
		chat = opts.Chat
		l2IDs = opts.L2IDs
		skipDistill = opts.SkipDistill
	}
	if llm == nil {
		llm = dream.NewOpenAIProvider(&m.config.LLM)
	}
	if chat == nil {
		if cp, ok := llm.(dream.ChatProvider); ok {
			chat = cp
		} else {
			chat = dream.BuildChatProvider(&m.config.LLM)
		}
	}
	return llm, chat, l2IDs, skipDistill
}

// parseL2IDs uses strict fail-fast semantics: any invalid hex ID aborts
// the entire operation and returns nil so Dream cannot execute with a
// partial ID set.
func parseL2IDs(ids []string) ([]uint64, error) {
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		h, err := hash.ParseID(id)
		if err != nil {
			return nil, fmt.Errorf("memhop: invalid L2 ID %q: %w", id, err)
		}
		out = append(out, h)
	}
	return out, nil
}
