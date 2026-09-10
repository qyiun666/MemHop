// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package plan holds the L5 plan-tree small methods: the status surface, the
// Step a host restates, the node create/update steps, and the forest build with
// its rollup. A plan is keyed by the turn that opened it, so the key itself is
// parsed by content.ParseTopicID, and a step inside it is addressed by a
// per-topic ordinal. The big methods (PlanCreate, PlanNodeAdd, PlanNodeUpdate,
// PlanState) stay in the composition root with the domain lock; a turn's events
// are L4 content, written by the content package rather than here.

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
// uint8 in a plan node whose meaning a reader has to interpret, so an undefined
// value is a corrupt record rather than a state to guess at: guessing would map
// a step the engine cannot name onto a state it never reached.
var statusNames = map[uint8]PlanStatus{
	core.StatusInProgress: PlanInProgress,
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

// Step is one node's restatement: which step of which turn, and where it got to.
// A blank Title or Summary inherits what the node already holds, so updating a
// step never rewinds its title or erases a folded summary. Status has no blank
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
