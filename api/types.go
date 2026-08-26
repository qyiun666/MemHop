// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package api is the public export layer of the MemHop memory engine.
//
// The implementation lives entirely under internal/ (packages internal,
// internal/repo/core and internal/common). This package re-exports only the
// surface required by the public method signatures — parameter and return
// types, enum constants and the error-code contract — as type aliases, so
// external hosts can construct arguments and name results without importing
// internal packages.
package api

import (
	"github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ---- config / encoder ----

type (
	// MemHopConfig configures a MemHop database; nested fields (LLM, Defaults)
	// are assigned by field access, see DefaultMemHopDefaults.
	MemHopConfig = internal.MemHopConfig
	// Encoder is the embedding encoder contract required by OpenWithEncoder.
	Encoder = internal.Encoder
	// LlmConfig holds LLM provider settings; exported so hosts can build
	// MemHopConfig.LLM by literal instead of field-by-field assignment.
	LlmConfig = internal.LlmConfig
	// MemHopDefaults holds the host-facing business knobs (Capacity,
	// DreamCompressMinTopics, SearchDreamContextThreshold); engine tuning
	// constants are package-private. Exported so hosts can name the type
	// instead of copying DefaultMemHopDefaults.
	MemHopDefaults = internal.MemHopDefaults
)

// DefaultMemHopDefaults is the shared default engine configuration; assign
// it to MemHopConfig.Defaults without naming the nested type.
var DefaultMemHopDefaults = internal.DefaultMemHopDefaults

// ---- internal-layer types (method signatures) ----

type (
	SearchQuery         = internal.SearchQuery
	SearchResult        = internal.SearchResult
	SceneContext        = internal.SceneContext
	L3Graph             = internal.L3Graph
	L3ImportItem        = internal.L3ImportItem
	L3ImportMode        = internal.L3ImportMode
	L3ImportResult      = internal.L3ImportResult
	L3NodeQuery         = internal.L3NodeQuery
	L3Subgraph          = internal.L3Subgraph
	L4Query             = internal.L4Query
	CapabilityListQuery = internal.CapabilityListQuery
	CapabilityPatch     = internal.CapabilityPatch
	CrystallizeResult   = internal.CrystallizeResult
	CrystallizeDetail   = internal.CrystallizeDetail
	TrajectoryStats     = internal.TrajectoryStats
)

// ---- core (L0-L6 slot models) ----

type (
	ProfileSlot    = core.ProfileSlot
	SceneSlot      = core.SceneSlot
	HypergraphSlot = core.HypergraphSlot
	HypergraphNode = core.HypergraphNode
	ArchiveSlot    = core.ArchiveSlot
	Capability     = core.Capability
	TrajectorySlot = core.TrajectorySlot
	GraphEdgeKind  = core.GraphEdgeKind
	// TopicSlot is an L2 dual-track session node (user/agent); the element
	// type of SearchResult.Contexts / AssociatedContexts.
	TopicSlot = core.TopicSlot
	// ResourceRef is one wrapped resource (an MCP tool or a skill) inside a
	// Capability; the element type of Capability.Resources.
	ResourceRef = core.ResourceRef
)

// ---- error contract ----

// Code is the numeric error-code type carried inside Error.
type Code = common.Code

// CodeOf extracts the numeric error code of err (0 when it is not a MemHop Error).
func CodeOf(err error) Code {
	return common.CodeOf(err)
}

// ---- L3 import mode constants ----

const (
	L3ImportSkip      = internal.L3ImportSkip
	L3ImportMerge     = internal.L3ImportMerge
	L3ImportOverwrite = internal.L3ImportOverwrite
)

// ---- L5 capability enum constants ----

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

// ---- common error code constants ----

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
