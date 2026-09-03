// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Re-exported data-model surface of the facade: enum constants and the error
// contract. Response slot models are real structs in api/types.go so their
// IDs are surfaced as 16-char hex strings.

package api

import "github.com/qyiun666/MemHop/internal"

// ---- L5 capability enum constants ----

const (
	CapabilityMCP       = internal.CapabilityMCP
	CapabilitySkill     = internal.CapabilitySkill
	CapabilityAPI       = internal.CapabilityAPI
	CapabilityComposite = internal.CapabilityComposite
)

const (
	CapabilityDraft      = internal.CapabilityDraft
	CapabilityActive     = internal.CapabilityActive
	CapabilityDeprecated = internal.CapabilityDeprecated
)

const (
	CapabilityOriginImported     = internal.CapabilityOriginImported
	CapabilityOriginCrystallized = internal.CapabilityOriginCrystallized
	CapabilityOriginHost         = internal.CapabilityOriginHost
	CapabilityOriginBuiltin      = internal.CapabilityOriginBuiltin
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

const (
	RoleUser   = internal.RoleUser
	RoleAgent  = internal.RoleAgent
	RoleSystem = internal.RoleSystem
	RoleDream  = internal.RoleDream
)

// ---- L6 trajectory node type constants ----

const (
	NodeTypeEvent = internal.NodeTypeEvent
	NodeTypePlan  = internal.NodeTypePlan
)

// Status* are the numeric codes carried by TrajectorySlot.Status, i.e.
// read-side only: every write path overwrites that field, so a host compares
// these against what ReadTrajectory returns and never passes them to a
// write. The plan write/query surface uses the string form below.
const (
	StatusPending    = internal.StatusPending
	StatusInProgress = internal.StatusInProgress
	StatusDone       = internal.StatusDone
	StatusFailed     = internal.StatusFailed
	StatusRunning    = internal.StatusRunning
)

// PlanStatus* are the string lifecycle values PlanCommit accepts and
// PlanState/ListPlans emit. They are assignable to the string parameter of
// PlanCommit; the numeric Status* constants above are a different encoding of
// the same states and are not interchangeable with them.
const (
	PlanStatusPending    PlanStatus = internal.PlanPending
	PlanStatusInProgress PlanStatus = internal.PlanInProgress
	PlanStatusRunning    PlanStatus = internal.PlanRunning
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
