// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Re-exported data-model surface of the facade: the enum constants and the error
// contract. Response shapes live in api/types.go.

package api

import "github.com/qyiun666/MemHop/internal"

// ---- agent domain identity ----

// AgentTypePrimary marks the domain a file is opened on — a file holds exactly one of
// them; AgentTypeSub marks a domain created under it. ProfileSlot.AgentType reports
// which. The library stamps it when the domain comes to exist, and it is not a field a
// host can send: ProfileInput has no place to put one.
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

// RoleUser / RoleAgent / RoleSystem are what a host may declare for an utterance it
// appends. RoleDream is not: Dream marks a fused group's summary with it, and the append
// boundary refuses it, so a host cannot write a record that reads as consolidated. The
// name is still exported because a read hands the value back — SceneContext's messages
// and ArchiveSlot.Role carry it on a fused group — and a host choosing what to put in
// front of the model has to be able to say which line the library wrote. Naming it grants
// no write ability: the refusal is enforced at the boundary, not by withholding the name.
// These are values of ArchiveRole, the same type the shapes above declare their field as,
// so a host assigns one with no conversion.
// The per-record write budgets, so a host can size what it appends instead of discovering
// the boundary through a refusal. `AppendArchive` refuses what exceeds them and never
// truncates: a shortened record reads as a complete one.
const (
	MaxEventPayloadBytes     = internal.MaxEventPayloadBytes
	MaxUtterancePayloadBytes = internal.MaxUtterancePayloadBytes
	// MaxSubAgentNameBytes is the cap on the name that addresses a sub-agent domain —
	// bytes, not runes, because that is what the record stores and what the check
	// measures. A host generating worker names from task titles needs this number to
	// shorten safely rather than after a refusal.
	MaxSubAgentNameBytes = internal.MaxSubAgentNameBytes
)

const (
	RoleUser   = internal.RoleUser
	RoleAgent  = internal.RoleAgent
	RoleSystem = internal.RoleSystem
	RoleDream  = internal.RoleDream
)

// PlanStatus* are the string lifecycle values the plan write surface accepts and
// PlanState emits. A created step starts as PlanStatusInProgress: the engine keeps no
// "planned but not started" state.
const (
	PlanStatusInProgress PlanStatus = internal.PlanInProgress
	PlanStatusDone       PlanStatus = internal.PlanDone
	PlanStatusFailed     PlanStatus = internal.PlanFailed
)

// ---- L4 content type constants ----

// These are the only valid ContentType values. A value outside them is refused where
// content is appended (Session.AppendArchive — the only write path that stores one) and
// where a read filters on one (SearchL4's Type condition): the write boundary would not
// store it, so a filter matching one would be answered as "this turn holds no such
// medium".
const (
	ContentText     = internal.ContentText
	ContentImage    = internal.ContentImage
	ContentVideo    = internal.ContentVideo
	ContentDocument = internal.ContentDocument
	ContentAudio    = internal.ContentAudio
	ContentCode     = internal.ContentCode
	ContentOther    = internal.ContentOther
)

// KindUtterance is something somebody said — an original or a Dream-fused summary;
// KindEvent is something that happened while they said it.
const (
	KindUtterance = internal.KindUtterance
	KindEvent     = internal.KindEvent
)
