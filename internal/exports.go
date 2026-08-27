// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Re-export seam for the api facade: the api package must not import
// internal/repo/core or internal/common directly (dependency chain
// api → internal → repo → core), so every slot model, enum constant and
// error-code symbol that appears in the public surface is re-exported
// here as an alias/forwarder. Aliases are identity — no copying, no
// business logic.

package internal

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ---- slot models (method signatures of Session / DB) ----

type (
	ProfileSlot    = core.ProfileSlot
	SceneSlot      = core.SceneSlot
	HypergraphSlot = core.HypergraphSlot
	HypergraphNode = core.HypergraphNode
	ArchiveSlot    = core.ArchiveSlot
	Capability     = core.Capability
	TrajectorySlot = core.TrajectorySlot
	GraphEdgeKind  = core.GraphEdgeKind
	TopicSlot      = core.TopicSlot
	ResourceRef    = core.ResourceRef
)

// ---- error contract ----

// Code is the numeric error-code type carried inside Error.
type Code = common.Code

// CodeOf extracts the numeric error code of err (0 when it is not a MemHop Error).
func CodeOf(err error) Code { return common.CodeOf(err) }

// Error codes; see internal/common/errors.go for the interval contract.
const (
	ErrConfig            = common.ErrConfig
	ErrVectorDimMismatch = common.ErrVectorDimMismatch
	ErrInvalidQuery      = common.ErrInvalidQuery
	ErrNotFound          = common.ErrNotFound
	ErrAgentNotFound     = common.ErrAgentNotFound
	ErrIO                = common.ErrIO
	ErrClosed            = common.ErrClosed
	ErrInvalidMagic      = common.ErrInvalidMagic
	ErrCRCMismatch       = common.ErrCRCMismatch
	ErrCorruption        = common.ErrCorruption
	ErrSerialization     = common.ErrSerialization
	ErrDeserialization   = common.ErrDeserialization
	ErrEncoder           = common.ErrEncoder
	ErrLLM               = common.ErrLLM
)

// ---- L5 capability enum constants ----

// Enum types of the constants below; re-exported so hosts can name
// pointer fields (e.g. CapabilityListQuery.Status) without importing core.
type (
	CapabilityType   = core.CapabilityType
	CapabilityStatus = core.CapabilityStatus
	CapabilityOrigin = core.CapabilityOrigin
)

const (
	CapabilityMCP       = core.CapabilityMCP
	CapabilitySkill     = core.CapabilitySkill
	CapabilityAPI       = core.CapabilityAPI
	CapabilityComposite = core.CapabilityComposite
)

const (
	CapabilityDraft      = core.CapabilityDraft
	CapabilityActive     = core.CapabilityActive
	CapabilityDeprecated = core.CapabilityDeprecated
)

const (
	CapabilityOriginImported     = core.CapabilityOriginImported
	CapabilityOriginCrystallized = core.CapabilityOriginCrystallized
	CapabilityOriginHost         = core.CapabilityOriginHost
	CapabilityOriginBuiltin      = core.CapabilityOriginBuiltin
)

// ---- L3 edge kind constants ----

const (
	EdgeRelated    = core.EdgeRelated
	EdgeCausal     = core.EdgeCausal
	EdgePartOf     = core.EdgePartOf
	EdgeSequence   = core.EdgeSequence
	EdgeDependency = core.EdgeDependency
	EdgeCustom     = core.EdgeCustom
)

// ---- external id rendering ----

// FormatAgentID renders an agentID as its external 16-char hex form.
func FormatAgentID(agentID uint64) string { return common.FormatHash(agentID) }

// ParseAgentID parses a 16-char hex agentID.
func ParseAgentID(s string) (uint64, error) { return common.ParseID(s) }
