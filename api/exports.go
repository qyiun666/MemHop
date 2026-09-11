// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Re-exported data-model surface of the facade: enum constants and the error
// contract. Response slot models are real structs in api/types.go so their
// IDs are surfaced as 16-char hex strings.

package api

import "github.com/qyiun666/MemHop/internal"

// ---- id surface ----

// DefaultAgentID is the 16-hex id of the implicit single-tenant domain: pass
// it to MultiAgentDB.Session to work in the default agent domain.
const DefaultAgentID = "0000000000000000"

// ---- agent domain identity ----

// AgentTypePrimary marks the domain a file is opened on, so a file holds exactly
// one of them; AgentTypeSub marks a domain created under it. ProfileSlot.AgentType
// reports which, and the library stamps it — a host write inherits the stored
// value rather than taking the caller's.
const (
	AgentTypePrimary = internal.AgentTypePrimary
	AgentTypeSub     = internal.AgentTypeSub
)

// ---- L3 edge kind constants ----

const (
	EdgeRelated    = internal.EdgeRelated
	EdgeCausal     = internal.EdgeCausal
	EdgePartOf     = internal.EdgePartOf
	EdgeSequence   = internal.EdgeSequence
	EdgeDependency = internal.EdgeDependency
	EdgeCustom     = internal.EdgeCustom
)

// ---- L3 import mode constants ----

const (
	L3ImportSkip      = internal.L3ImportSkip
	L3ImportMerge     = internal.L3ImportMerge
	L3ImportOverwrite = internal.L3ImportOverwrite
)

// ---- L4 message role constants ----

// These are the roles a host may declare for an utterance it appends. RoleDream is
// deliberately absent: Dream marks a fused group's summary with it, and the append
// boundary refuses it, so a host cannot write a record that reads as consolidated.
const (
	RoleUser   = internal.RoleUser
	RoleAgent  = internal.RoleAgent
	RoleSystem = internal.RoleSystem
)

// PlanStatus* are the string lifecycle values the plan write surface accepts and
// PlanState emits. A step created by the plan write surface starts as
// PlanStatusInProgress: the engine keeps no "planned but not started" state. A
// node's status is only ever expressed this way — the L4 event record carries no
// status field, because every write path assigns the node's own state separately
// from the events bound to it.
const (
	PlanStatusInProgress PlanStatus = internal.PlanInProgress
	PlanStatusDone       PlanStatus = internal.PlanDone
	PlanStatusFailed     PlanStatus = internal.PlanFailed
)

// ---- L4 content type constants ----

// These are the only valid ContentType values; Update rejects a turn whose
// user_type or agent_type is anything else.
const (
	ContentText     = internal.ContentText
	ContentImage    = internal.ContentImage
	ContentVideo    = internal.ContentVideo
	ContentDocument = internal.ContentDocument
	ContentAudio    = internal.ContentAudio
	ContentCode     = internal.ContentCode
	ContentOther    = internal.ContentOther
)

// KindUtterance and KindEvent tell a topic's L4 content apart: what somebody
// said, versus what happened while they said it. SearchL4's Kind is a condition
// like any other, so leaving it unset selects both.
const (
	KindUtterance = internal.KindUtterance
	KindEvent     = internal.KindEvent
)
