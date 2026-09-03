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

// TrajectoryRetention bounds the L6 event log: Dream drops events older
// than this. L6 is a process index — durable products live in L4/L5.
const TrajectoryRetention = 7 * 24 * time.Hour

// PruneTrajectoryStage drops L6 trajectory events older than the retention
// window; durable products live in L4/L5, so L6 stays a bounded process
// index. Best-effort: a failure is logged and recorded in the report but
// never aborts Dream. Callers hold ac.Mu.
func PruneTrajectoryStage(ac *domain.Context, agentID uint64, rep *core.DreamReport) {
	start := time.Now()
	cutoff := time.Now().Add(-TrajectoryRetention).UnixMilli()
	hashes := ac.Traj.RemoveBefore(cutoff)
	var err error
	if len(hashes) > 0 {
		if _, err = repo.DeleteTrajectoryByIDs(ac.Engine, agentID, hashes); err != nil {
			slog.Warn("dream: trajectory prune failed", "agent", common.FormatHash(agentID), "err", err)
		}
	}
	// Plan nodes sit outside the event TrajIndex, so sweep them by their own
	// timestamp from the engine (authoritative — Dream is a disk maintainer,
	// not a hot path). A plan is exempt only while it BOTH holds a non-Done
	// node AND saw activity inside the retention window: an in-flight task
	// must not lose its tree mid-task, but once a plan has been silent past
	// the window it is abandoned and sweeps like any other record, so L6
	// stays bounded. Expired nodes of the swept plans cascade their bound
	// events so no orphan PlanNodeRef survives. The in-memory planCache is
	// refreshed only after the disk sweep succeeds, keeping cache and engine
	// in sync. Cascade-deleted events that are still fresh may linger in the
	// TrajIndex until the periodic prune or a context rebuild; readers skip
	// missing records, so the drift is benign.
	type pruneDel struct {
		planID   uint64
		nodeDel  []uint64
		eventDel []uint64
	}
	var prunes []pruneDel
	var delIDs []uint64
	for _, agg := range repo.CollectPlanAggregates(ac.Engine, agentID) {
		if agg.HasNonDone && agg.LastActiveAt >= cutoff {
			continue
		}
		var nodeDel []uint64
		for _, n := range agg.Nodes {
			if n.Timestamp < cutoff {
				nodeDel = append(nodeDel, n.IDHash)
			}
		}
		if len(nodeDel) == 0 {
			continue
		}
		expired := make(map[uint64]struct{}, len(nodeDel))
		for _, id := range nodeDel {
			expired[id] = struct{}{}
		}
		var eventDel []uint64
		for _, ev := range agg.Events {
			if _, ok := expired[ev.PlanNodeRef]; ok {
				eventDel = append(eventDel, ev.IDHash)
			}
		}
		delIDs = append(delIDs, nodeDel...)
		delIDs = append(delIDs, eventDel...)
		prunes = append(prunes, pruneDel{planID: agg.PlanID, nodeDel: nodeDel, eventDel: eventDel})
	}
	if len(delIDs) > 0 {
		if _, derr := repo.DeleteTrajectoryByIDs(ac.Engine, agentID, delIDs); derr != nil {
			slog.Warn("dream: plan-node prune failed", "agent", common.FormatHash(agentID), "err", derr)
		} else {
			for _, p := range prunes {
				ac.Plans.RemovePlanIDs(p.planID, p.nodeDel, p.eventDel)
			}
		}
	}
	AppendStage(rep, "l6_prune", start, err)
}
