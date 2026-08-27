// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Re-exported data-model surface of the facade: L0–L6 slot models and the
// enum constants accepted by the public methods. Every symbol forwards to
// the internal re-export seam (internal/exports.go); the underlying
// definitions live in internal/repo/core.

package api

import "github.com/qyiun666/MemHop/internal"

// ---- slot models (L0–L6) ----

type (
	ProfileSlot    = internal.ProfileSlot
	SceneSlot      = internal.SceneSlot
	HypergraphSlot = internal.HypergraphSlot
	HypergraphNode = internal.HypergraphNode
	ArchiveSlot    = internal.ArchiveSlot
	Capability     = internal.Capability
	TrajectorySlot = internal.TrajectorySlot
	GraphEdgeKind  = internal.GraphEdgeKind
	// TopicSlot is an L2 dual-track session node (user/agent); the element
	// type of SearchResult.Contexts / AssociatedContexts.
	TopicSlot = internal.TopicSlot
	// ResourceRef is one wrapped resource (an MCP tool or a skill) inside a
	// Capability; the element type of Capability.Resources.
	ResourceRef = internal.ResourceRef
)

// ---- L5 capability enum constants ----

// Enum types accepted by Capability fields and CapabilityListQuery /
// CapabilityPatch pointer fields.
type (
	CapabilityType   = internal.CapabilityType
	CapabilityStatus = internal.CapabilityStatus
	CapabilityOrigin = internal.CapabilityOrigin
)

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
