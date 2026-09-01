// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// agentContext is the per-agent business state: the L2Meta topic cache, the
// L6 indices, Dream bookkeeping and its own lock. Same-agent operations are
// serialized on mu (inheriting the single-instance serial contract);
// different agents run in parallel.
type agentContext struct {
	id uint64
	mu sync.Mutex // domain lock: same agent serial, across agents parallel

	l2Meta        *index.L2MetaIndex  // L2 topic metadata cache (serves the scene read)
	traj          *index.TrajIndex    // L6 turn trajectory shape (Seq/hash/timestamp/topic)
	plans         *planCache          // L6 plan->nodes/events aggregate (no engine scan per op)
	dreamInFlight map[uint64]struct{} // scenes with a scheduled background Dream

	lastDreamAt  atomic.Int64 // Unix ms of the last successful Dream (0 = never)
	lastActiveAt atomic.Int64 // Unix ms of the last context access (idle sweep)
	deleted      atomic.Bool  // DeleteAgent tombstone: contextFor rejects a destroyed domain

	// opCtx bounds the agent's cancellable work: background Dream pipelines
	// and foreground LLM calls made while holding mu (Update's turn
	// distillation). DeleteAgent, the idle sweep and Close cancel it so
	// pending work exits promptly instead of blocking the lifecycle barriers
	// for a full LLM round-trip.
	opCtx    context.Context
	opCancel context.CancelFunc
}

// newAgentContext builds one domain's state with every cache restored from
// its own records; a reclaimed or fresh domain rebuilds here.
func newAgentContext(id uint64, parent context.Context, engine *core.StorageEngine) *agentContext {
	ctx, cancel := context.WithCancel(parent)
	return &agentContext{
		id:            id,
		l2Meta:        index.BuildL2MetaFromEngine(engine, id),
		traj:          index.BuildTrajFromEngine(engine, id),
		plans:         buildPlanCache(engine, id),
		dreamInFlight: make(map[uint64]struct{}),
		opCtx:         ctx,
		opCancel:      cancel,
	}
}
