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

// ---- L5 capability constants ----

const (
	CapabilityMCP       = internal.CapabilityMCP
	CapabilitySkill     = internal.CapabilitySkill
	CapabilityAPI       = internal.CapabilityAPI
	CapabilityComposite = internal.CapabilityComposite
)

// CapabilityFormatV4 is the format string a memhop-capability/v4 document
// must declare.
const CapabilityFormatV4 = internal.CapabilityFormatV4

// ParseCapabilityPackage parses and validates one memhop-capability/v4
// package document (the JSON bytes of a capability.json) into its capability
// cards. The engine stores no capability records — a host owns its capability
// directory, reads each capability.json and reuses this parser as the disk
// format's single source of truth.
func ParseCapabilityPackage(data []byte, source string) ([]CapabilityImport, error) {
	return internal.ParseCapabilityPackage(data, source)
}

// ValidateCapabilityCard checks one capability card against the v4 rules
// (name, trigger or summary, resource shape, action-chain tool keys).
func ValidateCapabilityCard(card *CapabilityImport) error {
	return internal.ValidateCapabilityCard(card)
}

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

// These are the roles the engine writes: Update stores the turn's two originals
// as RoleUser / RoleAgent, Dream stores a fused group's summary as RoleDream.
const (
	RoleUser  = internal.RoleUser
	RoleAgent = internal.RoleAgent
	RoleDream = internal.RoleDream
)

// PlanStatus* are the string lifecycle values PlanCommit accepts and
// PlanState emits. A node's status is only ever expressed this way: the L6
// event record carries no status field, because every write path assigns the
// node's own state separately from the events bound to it.
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
