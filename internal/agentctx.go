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

	// dreamCtx owns the background Dream pipelines of this agent; DeleteAgent
	// and Close cancel it so an in-flight Dream exits at its next stage
	// boundary instead of writing to a destroyed domain.
	dreamCtx    context.Context
	dreamCancel context.CancelFunc
}

func newAgentContext(id uint64, parent context.Context) *agentContext {
	ctx, cancel := context.WithCancel(parent)
	return &agentContext{
		id:            id,
		dreamInFlight: make(map[uint64]struct{}),
		dreamCtx:      ctx,
		dreamCancel:   cancel,
	}
}

// dreamBusy reports whether any background Dream is scheduled for this
// domain; the idle sweep never reclaims a busy domain.
func (ac *agentContext) dreamBusy() bool {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return len(ac.dreamInFlight) > 0
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
