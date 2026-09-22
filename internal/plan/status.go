// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package plan holds the L5 plan-tree small methods: the status surface, the two
// input shapes a write step takes, the create/update steps, and the forest build
// with its rollup. A plan is keyed by one topic and a step inside it is addressed
// by a per-topic ordinal; this package writes plan node records and nothing else.

package plan

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// PlanStatus is the string surface of a plan node's lifecycle.
type PlanStatus string

const (
	PlanInProgress PlanStatus = "in_progress"
	PlanDone       PlanStatus = "done"
	PlanFailed     PlanStatus = "failed"
)

// statusNames is the one table both directions read. Status is the only bare
// uint8 whose meaning a reader has to interpret, so an undefined value is a
// corrupt record, not a state to guess at.
var statusNames = map[uint8]PlanStatus{
	core.StatusInProgress: PlanInProgress,
	core.StatusDone:       PlanDone,
	core.StatusFailed:     PlanFailed,
}

// StatusToU8 resolves a caller-supplied status, refusing anything the table does
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

// Step is one node's restatement: which step of which turn, and where it got to.
// A blank Title or Summary inherits what the node holds; Status has no blank
// meaning — every update states it.
type Step struct {
	TopicID uint64
	Seq     uint32
	Status  PlanStatus
	Title   string
	Summary string
}

// NodeSpec names one step to create: the turn that owns the tree, the step it
// hangs under, and its title. A zero ParentSeq makes it a root.
type NodeSpec struct {
	TopicID   uint64
	ParentSeq uint32
	Title     string
}

// IsTerminalStatus reports whether a plan-node status is a final state (done
// or failed); only these record a FinishedAt.
func IsTerminalStatus(u uint8) bool {
	return u == core.StatusDone || u == core.StatusFailed
}
