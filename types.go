// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"github.com/qyiun666/MemHop/internal/sub"
	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// ---- sub layer types ----

// LlmConfig holds LLM provider settings.
type LlmConfig = sub.LlmConfig

// MemHopDefaults holds all tunable engine defaults.
type MemHopDefaults = sub.MemHopDefaults

// SearchQuery is the retrieval request fed to (*DB).Search.
type SearchQuery = sub.SearchQuery

// SearchResult is the retrieval response returned by (*DB).Search.
type SearchResult = sub.SearchResult

// L3Graph is an L3 hypergraph view (slot + nodes + edges).
type L3Graph = sub.L3Graph

// L3ImportItem is one knowledge node to import.
type L3ImportItem = sub.L3ImportItem

// L3ImportMode controls duplicate handling in ImportL3.
type L3ImportMode = sub.L3ImportMode

// L3ImportResult reports the outcome of ImportL3.
type L3ImportResult = sub.L3ImportResult

// L3NodeQuery is a node lookup query.
type L3NodeQuery = sub.L3NodeQuery

// L3Subgraph is a BFS subgraph view.
type L3Subgraph = sub.L3Subgraph

// L4Query is an archive query.
type L4Query = sub.L4Query

// PluginImport is the JSON description of a plugin read from an import path.
type PluginImport = sub.PluginImport

// PluginListQuery filters the L5 plugin list.
type PluginListQuery = sub.PluginListQuery

// CrystallizeResult reports the L5 plugins created from a trajectory.
type CrystallizeResult = sub.CrystallizeResult

// ---- core (L0-L7 slot models) types ----

// ContentType is the type of content stored in an ArchiveSlot.
type ContentType = core.ContentType

// PluginStatus is the lifecycle state of a PluginSlot.
type PluginStatus = core.PluginStatus

// SourceKind identifies how an L3 HypergraphSlot was created.
type SourceKind = core.SourceKind

// GraphEdgeKind classifies edges within an L3 hypergraph.
type GraphEdgeKind = core.GraphEdgeKind

// ProfileSlot is the L0 profile singleton.
type ProfileSlot = core.ProfileSlot

// SceneSlot is an L2 scene container.
type SceneSlot = core.SceneSlot

// TopicSlot is an L2 dual-track session node.
type TopicSlot = core.TopicSlot

// HypergraphSource is the origin of an L3 hypergraph.
type HypergraphSource = core.HypergraphSource

// HypergraphSlot is an L3 hypergraph container slot.
type HypergraphSlot = core.HypergraphSlot

// HypergraphNode is a node within an L3 hypergraph.
type HypergraphNode = core.HypergraphNode

// HypergraphEdge is an edge within an L3 hypergraph.
type HypergraphEdge = core.HypergraphEdge

// ArchiveSlot is an L4 user/agent chat message.
type ArchiveSlot = core.ArchiveSlot

// PluginSlot is an L5 plugin capability package.
type PluginSlot = core.PluginSlot

// PluginManifest is the structured content of a PluginSlot.
type PluginManifest = core.PluginManifest

// PluginItem is one entry within a plugin manifest section.
type PluginItem = core.PluginItem

// SceneUsageSlot is an L6 scene-level retrieval usage feedback record.
type SceneUsageSlot = core.SceneUsageSlot

// TrajectorySlot is an L7 operation trajectory event.
type TrajectorySlot = core.TrajectorySlot

// ---- sub L3 import mode constants ----

const (
	L3ImportSkip      = sub.L3ImportSkip
	L3ImportMerge     = sub.L3ImportMerge
	L3ImportOverwrite = sub.L3ImportOverwrite
)

// ---- core enum constants ----

const (
	ContentText     = core.ContentText
	ContentImage    = core.ContentImage
	ContentVideo    = core.ContentVideo
	ContentDocument = core.ContentDocument
	ContentAudio    = core.ContentAudio
	ContentCode     = core.ContentCode
	ContentOther    = core.ContentOther
)

const (
	PluginDraft      = core.PluginDraft
	PluginActive     = core.PluginActive
	PluginDeprecated = core.PluginDeprecated
)

const (
	SourcePath    = core.SourcePath
	SourceContext = core.SourceContext
	SourceURL     = core.SourceURL
	SourceManual  = core.SourceManual
)

const (
	EdgeRelated    = core.EdgeRelated
	EdgeCausal     = core.EdgeCausal
	EdgePartOf     = core.EdgePartOf
	EdgeSequence   = core.EdgeSequence
	EdgeDependency = core.EdgeDependency
	EdgeCustom     = core.EdgeCustom
)

// ArchiveSlot message roles.
const (
	RoleUser   = core.RoleUser
	RoleAgent  = core.RoleAgent
	RoleSystem = core.RoleSystem
	RoleDream  = core.RoleDream
)

// ---- common error code constants ----

const (
	ErrConfig            = common.ErrConfig
	ErrVectorDimMismatch = common.ErrVectorDimMismatch
	ErrInvalidQuery      = common.ErrInvalidQuery
	ErrNotFound          = common.ErrNotFound
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

// DefaultMemHopDefaults is the shared default engine configuration.
var DefaultMemHopDefaults = sub.DefaultMemHopDefaults
