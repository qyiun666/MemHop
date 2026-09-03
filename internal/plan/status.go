// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package plan holds the L6 plan-tree small methods: the status surface,
// node-path mechanics, the node/event write steps, the forest build and
// rollup, and the whole-tree sync. The big methods (PlanAppend, PlanCommit,
// PlanState, PlanReplace, ListPlans, SyncPlanTree) stay in the composition
// root with the domain lock.

package plan

import (
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// PlanStatus is the string surface of a plan node's lifecycle.
type PlanStatus string

const (
	PlanPending    PlanStatus = "pending"
	PlanInProgress PlanStatus = "in_progress"
	PlanRunning    PlanStatus = "running"
	PlanDone       PlanStatus = "done"
	PlanFailed     PlanStatus = "failed"
)

func StatusToU8(s PlanStatus) (uint8, error) {
	switch s {
	case PlanPending:
		return core.StatusPending, nil
	case PlanInProgress:
		return core.StatusInProgress, nil
	case PlanRunning:
		return core.StatusRunning, nil
	case PlanDone:
		return core.StatusDone, nil
	case PlanFailed:
		return core.StatusFailed, nil
	default:
		return 0, common.NewError(common.ErrInvalidQuery, "invalid plan status: "+string(s))
	}
}

func StatusToString(u uint8) PlanStatus {
	switch u {
	case core.StatusPending:
		return PlanPending
	case core.StatusInProgress:
		return PlanInProgress
	case core.StatusRunning:
		return PlanRunning
	case core.StatusDone:
		return PlanDone
	case core.StatusFailed:
		return PlanFailed
	default:
		return PlanPending
	}
}

// IsTerminalStatus reports whether a plan-node status is a final state (done
// or failed); only these record a FinishedAt.
func IsTerminalStatus(u uint8) bool {
	return u == core.StatusDone || u == core.StatusFailed
}

// ParsePlanID parses a host plan id and rejects 0: AppendTrajectory writes
// bare turn events with PlanID=0, so 0 is a reserved sentinel and never a
// valid plan. Accepting it would let PlanReplace delete every bare event of
// the domain (DeletePlanRecords matches those records).
func ParsePlanID(planID string) (uint64, error) {
	ph, err := common.ParseID(planID)
	if err != nil {
		return 0, common.NewError(common.ErrInvalidQuery, "parse plan id", err)
	}
	if ph == 0 {
		return 0, common.NewError(common.ErrInvalidQuery, "plan id 0000000000000000 is reserved")
	}
	return ph, nil
}

func SplitNodePath(nodePath string) ([]string, error) {
	if nodePath == "" {
		return nil, common.NewError(common.ErrInvalidQuery, "nodePath required")
	}
	parts := strings.Split(nodePath, ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			return nil, common.NewError(common.ErrInvalidQuery, "invalid nodePath: "+nodePath)
		}
		out = append(out, p)
	}
	return out, nil
}
