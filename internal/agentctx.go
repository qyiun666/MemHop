// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/qyiun666/MemHop/internal/repo/index"
)

// agentContext is the per-agent business state: retrieval indices, active
// scenes, Dream bookkeeping and its own lock. Same-agent operations are
// serialized on mu (inheriting the single-instance serial contract);
// different agents run in parallel.
type agentContext struct {
	id uint64
	mu sync.Mutex // domain lock: same agent serial, across agents parallel

	sparseIndex   *index.SparseIndex  // BM25 (domain-local IDF/avgDocLength)
	l2Meta        *index.L2MetaIndex  // L2 topic metadata cache
	activeScenes  []uint64            // insertion-ordered active scene list
	dreamInFlight map[uint64]struct{} // scenes with a scheduled background Dream

	lastDreamAt  atomic.Int64 // Unix ms of the last successful Dream (0 = never)
	lastActiveAt atomic.Int64 // Unix ms of the last context access (idle sweep)
	deleted      atomic.Bool  // DeleteAgent tombstone: contextFor rejects a destroyed domain

	// opCtx bounds the agent's cancellable work: background Dream pipelines
	// and foreground LLM calls made while holding mu (Update's keyword
	// extraction). DeleteAgent, the idle sweep and Close cancel it so pending
	// work exits promptly instead of blocking the lifecycle barriers for a
	// full LLM round-trip.
	opCtx    context.Context
	opCancel context.CancelFunc
}

func newAgentContext(id uint64, parent context.Context) *agentContext {
	ctx, cancel := context.WithCancel(parent)
	return &agentContext{
		id:            id,
		dreamInFlight: make(map[uint64]struct{}),
		opCtx:         ctx,
		opCancel:      cancel,
	}
}

// activateScene appends a scene idempotently; repeats keep first-order
// positions. The active set is unbounded here: Update triggers a Dream on
// the oldest scene when it reaches Defaults.Capacity, and RunDream removes
// compressed scenes to bring it back down.
func (ac *agentContext) activateScene(sceneID uint64) {
	if slices.Contains(ac.activeScenes, sceneID) {
		return
	}
	ac.activeScenes = append(ac.activeScenes, sceneID)
}

// dropActiveScenes removes the given scenes from the active set, keeping
// insertion order of the survivors. Callers must hold ac.mu.
func (ac *agentContext) dropActiveScenes(ids map[uint64]struct{}) {
	if len(ids) == 0 {
		return
	}
	ac.activeScenes = slices.DeleteFunc(ac.activeScenes, func(sid uint64) bool {
		_, drop := ids[sid]
		return drop
	})
}
