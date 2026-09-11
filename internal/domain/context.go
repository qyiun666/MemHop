// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package domain carries one agent domain's state: the Context container
// (per-domain lock, the caches, a cancellable work context) plus the L2Meta and
// plan cache maintenance every write path shares. Engine, LLM transport, knobs
// and the three caches all hang off Context, which is what a write path reads
// them through.

package domain

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/config"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// Context is the per-agent state: the L2Meta topic cache, the L4 content mirror,
// the L5 plan cache, Dream bookkeeping and its own lock. Same-agent operations
// are serialized on Mu (inheriting the single-instance serial contract);
// different agents run in parallel. Callers reach every field only while holding
// Mu.
type Context struct {
	ID uint64
	Mu sync.Mutex // domain lock: same agent serial, across agents parallel

	Engine   *core.StorageEngine
	LLM      llmops.Chat
	Defaults *config.MemHopDefaults

	L2Meta        *index.L2MetaIndex  // L2 topic metadata cache
	L4            *index.L4Index      // content each topic owns: utterances AND events
	Plans         *PlanCache          // L5 plan tree per topic (no engine scan per op)
	DreamInFlight map[uint64]struct{} // scenes with a scheduled background Dream

	LastActiveAt atomic.Int64 // Unix ms of the last context access (idle sweep)

	// OpCtx bounds the agent's cancellable work: the long pipelines and the LLM
	// calls made while Mu is held. Cancelling it lets in-flight work exit at the
	// next stage boundary instead of blocking a lifecycle barrier for a full LLM
	// round-trip.
	OpCtx    context.Context
	OpCancel context.CancelFunc
}

// NewContext builds one domain's state with every cache restored from
// its own records; a reclaimed or fresh domain rebuilds here.
func NewContext(id uint64, parent context.Context, engine *core.StorageEngine, llm llmops.Chat, defaults *config.MemHopDefaults) *Context {
	ctx, cancel := context.WithCancel(parent)
	return &Context{
		ID:            id,
		Engine:        engine,
		LLM:           llm,
		Defaults:      defaults,
		L2Meta:        index.BuildL2MetaFromEngine(engine, id),
		L4:            index.BuildL4FromEngine(engine, id),
		Plans:         buildPlanCache(engine, id),
		DreamInFlight: make(map[uint64]struct{}),
		OpCtx:         ctx,
		OpCancel:      cancel,
	}
}
