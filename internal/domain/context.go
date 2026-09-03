// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package domain carries one agent domain's business state: the Context
// container (per-domain lock, caches, cancellable work context) plus the
// L2Meta / plan cache maintenance that every write path shares. The
// composition root owns the registry and the locking discipline; the
// small-method packages read engines, LLM transport, knobs and caches
// through Context, so none of them imports the root.

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

// Context is the per-agent business state: the L2Meta topic cache, the
// L6 indices, Dream bookkeeping and its own lock. Same-agent operations are
// serialized on Mu (inheriting the single-instance serial contract);
// different agents run in parallel. Callers reach every field only while
// holding Mu (the composition root takes it before dispatching).
type Context struct {
	ID uint64
	Mu sync.Mutex // domain lock: same agent serial, across agents parallel

	Engine   *core.StorageEngine
	LLM      llmops.Chat
	Defaults *config.MemHopDefaults

	L2Meta        *index.L2MetaIndex  // L2 topic metadata cache (serves the scene read)
	Traj          *index.TrajIndex    // L6 turn trajectory shape (Seq/hash/timestamp/topic)
	Plans         *PlanCache          // L6 plan->nodes/events aggregate (no engine scan per op)
	DreamInFlight map[uint64]struct{} // scenes with a scheduled background Dream

	LastActiveAt atomic.Int64 // Unix ms of the last context access (idle sweep)
	Deleted      atomic.Bool  // DeleteAgent tombstone: contextFor rejects a destroyed domain

	// OpCtx bounds the agent's cancellable work: background Dream pipelines
	// and foreground LLM calls made while holding Mu (Update's turn
	// distillation). DeleteAgent, the idle sweep and Close cancel it so
	// pending work exits promptly instead of blocking the lifecycle barriers
	// for a full LLM round-trip.
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
		Traj:          index.BuildTrajFromEngine(engine, id),
		Plans:         buildPlanCache(engine, id),
		DreamInFlight: make(map[uint64]struct{}),
		OpCtx:         ctx,
		OpCancel:      cancel,
	}
}
