// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"log/slog"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ContentRetention bounds how long a turn's records outlive it: Dream drops L4
// content older than this, and plan nodes past it too. Both layers share the one
// window because both hold the same thing — what happened in one turn — and a
// topic that keeps neither originals nor events is left with the keyword track
// that Dream folded out of them, which is the durable product.
const ContentRetention = 7 * 24 * time.Hour

// PruneContentStage drops the L4 records past the retention window, utterances
// and events alike, and reports how many went away. Best-effort: a failure is
// logged and recorded in the report but never aborts Dream. Callers hold ac.Mu.
func PruneContentStage(ac *domain.Context, agentID uint64, rep *core.DreamReport) {
	start := time.Now()
	cutoff := time.Now().Add(-ContentRetention).UnixMilli()
	dropped, err := repo.DropExpiredArchives(ac.Engine, agentID, ac.L4, cutoff)
	if err != nil {
		slog.Warn("dream: content prune failed", "agent", common.FormatHash(agentID), "err", err)
	} else if dropped > 0 {
		slog.Info("dream: content pruned", "agent", common.FormatHash(agentID), "records", dropped)
	}
	AppendStage(rep, "l4_prune", start, err)
}

// PrunePlanStage sweeps plan nodes past the window. A plan is exempt only while
// it BOTH holds a non-Done node AND saw activity inside the window: an in-flight
// task must not lose its tree mid-task, but once a plan has been silent past the
// window it is abandoned and sweeps like any other record, so L5 stays bounded.
//
// Nodes are swept on their own clock and touch no content: a step expiring takes
// the tree with it and leaves the turn's events exactly where they are.
// Best-effort, like the content stage. Callers hold ac.Mu.
func PrunePlanStage(ac *domain.Context, agentID uint64, rep *core.DreamReport) {
	start := time.Now()
	cutoff := time.Now().Add(-ContentRetention).UnixMilli()
	type sweep struct {
		topicID uint64
		ids     []uint64
	}
	var sweeps []sweep
	var doomed []uint64
	// The engine is read rather than the cache: Dream is a disk maintainer, not a
	// hot path, and the sweep must not be shaped by a cache that could be behind.
	aggs, err := repo.CollectPlanNodes(ac.Engine, agentID)
	if err != nil {
		// The exemption this pass reads is per-tree and derived from every node in
		// it, so an unreadable node is exactly the one that could still be holding a
		// tree alive. Skipping the sweep costs one Dream cycle; sweeping on a partial
		// set costs the tree.
		slog.Warn("dream: plan nodes not swept", "agent", common.FormatHash(agentID), "err", err)
		AppendStage(rep, "l5_prune", start, err)
		return
	}
	for _, agg := range aggs {
		if agg.HasNonDone && agg.LastActiveAt >= cutoff {
			continue
		}
		var ids []uint64
		for _, n := range agg.Nodes {
			if n.UpdatedAt < cutoff {
				ids = append(ids, n.IDHash)
			}
		}
		if len(ids) == 0 {
			continue
		}
		sweeps = append(sweeps, sweep{topicID: agg.TopicID, ids: ids})
		doomed = append(doomed, ids...)
	}
	if len(doomed) > 0 {
		if _, err = repo.DeletePlanNodesByIDs(ac.Engine, agentID, doomed); err != nil {
			slog.Warn("dream: plan-node prune failed", "agent", common.FormatHash(agentID), "err", err)
		} else {
			for _, s := range sweeps {
				ac.Plans.RemoveNodes(s.topicID, s.ids)
			}
			slog.Info("dream: plan nodes pruned", "agent", common.FormatHash(agentID), "nodes", len(doomed))
		}
	}
	AppendStage(rep, "l5_prune", start, err)
}
