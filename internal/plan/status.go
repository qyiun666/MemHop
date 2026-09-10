// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package plan holds the L6 plan-tree small methods: the status surface and
// Step, node-path mechanics, the node write steps, and the forest build with its
// rollup. A plan is keyed by the turn that opened it, so the key itself is
// parsed by content.ParseTopicID. The big methods (PlanCommit, PlanState) stay in
// the composition root with the domain lock; a turn's events are L4 content,
// written by the content package rather than here.

package plan

import (
	"fmt"
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

// statusNames is the one table both directions read. Status is the only bare
// uint8 in a plan node whose meaning a reader has to interpret, so an undefined
// value is a corrupt record rather than a state to guess at: guessing would turn
// a step the engine cannot name into a pending one, which is exactly the
// difference between "not started" and "failed".
var statusNames = map[uint8]PlanStatus{
	core.StatusPending:    PlanPending,
	core.StatusInProgress: PlanInProgress,
	core.StatusRunning:    PlanRunning,
	core.StatusDone:       PlanDone,
	core.StatusFailed:     PlanFailed,
}

// StatusToU8 resolves a host-supplied status, refusing anything the table does
// not name.
func StatusToU8(s PlanStatus) (uint8, error) {
	for u, name := range statusNames {
		if name == s {
			return u, nil
		}
	}
	return 0, common.NewError(common.ErrInvalidQuery, "invalid plan status: "+string(s))
}

// StatusToString renders a stored status for the surface; an unknown code is
// reported rather than mapped onto a default.
func StatusToString(u uint8) (PlanStatus, error) {
	s, ok := statusNames[u]
	if !ok {
		return "", common.NewError(common.ErrDeserialization,
			fmt.Sprintf("plan node carries undefined status %d", u))
	}
	return s, nil
}

// Step is one host commit's node-side fields. Status is the string surface; a
// blank Title/PlanType/Summary inherits what the node already holds, so
// committing a step again never rewinds its title or erases a folded summary.
type Step struct {
	Status   PlanStatus
	Title    string
	PlanType string // plan/step/tool_call; empty = plain node
	Summary  string
}

// IsTerminalStatus reports whether a plan-node status is a final state (done
// or failed); only these record a FinishedAt.
func IsTerminalStatus(u uint8) bool {
	return u == core.StatusDone || u == core.StatusFailed
}

// SplitNodePath breaks a dotted node path into its segments, refusing an empty
// path and any path with a blank segment ("1..2", a trailing dot).
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
