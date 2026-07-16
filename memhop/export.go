// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package memhop is the public facade for the MemHop memory database.
// It re-exports only the types and functions needed by external consumers.
// All internal implementation lives in github.com/qyiun666/memhop/internal/core.
package memhop

import (
	"github.com/qyiun666/memhop/memhop/internal/hash"
	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/dream"
	"github.com/qyiun666/memhop/memhop/internal/core/encoder"
	"github.com/qyiun666/memhop/memhop/internal/core/l3"
	"github.com/qyiun666/memhop/memhop/internal/core/l3/dsl"
	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/query"
)

// --- Config types ---

type Config = core.MemHopConfig

type ConfigDefaults = core.MemHopDefaults

var DefaultDefaults = core.DefaultMemHopDefaults

// --- Error types ---

var (
	ErrIO              = core.ErrIO
	ErrInvalidMagic    = core.ErrInvalidMagic
	ErrCRCMismatch     = core.ErrCRCMismatch
	ErrCorruption      = core.ErrCorruption
	ErrNotFound        = core.ErrNotFound
	ErrVectorDimMismatch = core.ErrVectorDimMismatch
	ErrSerialization   = core.ErrSerialization
	ErrDeserialization = core.ErrDeserialization
	ErrEncoder         = core.ErrEncoder
	ErrConfig          = core.ErrConfig
	ErrLLM             = core.ErrLLM
	ErrInvalidQuery    = core.ErrInvalidQuery
	ErrClosed          = core.ErrClosed
)

type Error = core.MemHopError

var NewError = core.NewError

// --- Search types ---

type SearchQuery = query.SearchQuery

type SearchResult = query.SearchResult

type SearchDefaults = query.SearchDefaults

type ContextResult = query.ContextResult

type ProfileResult = query.ProfileResult

type L1Preview = query.L1Preview

type L3Preview = query.L3Preview

type L3SearchQuery = query.L3SearchQuery

type L3SearchResult = query.L3SearchResult

type L3EntityHint = query.L3EntityHint

type SearchPreprocessResult = query.SearchPreprocessResult

type WritePreprocessResult = query.WritePreprocessResult

// --- CRUD types ---

type TopicListQuery = query.TopicListQuery

type TopicListResult = query.TopicListResult

type TopicSummary = query.TopicSummary

type TopicDetail = query.TopicDetail

type L3Detail = query.L3Detail

type GraphNode = query.GraphNode

type GraphEdge = query.GraphEdge

type Subgraph = query.Subgraph

type TraversalHop = query.TraversalHop

type MergeResult = query.MergeResult

type SceneTreeResult = query.SceneTreeResult

type L1Graph = query.L1Graph

type L1Node = query.L1Node

type L1Edge = query.L1Edge

// --- L3 types ---

type CommunityConfig = l3.CommunityConfig

type CommunityResult = l3.CommunityResult

var DefaultCommunityConfig = l3.DefaultCommunityConfig

// --- Archive types ---

type ArchiveQuery = query.ArchiveQuery

type ArchiveListResult = query.ArchiveListResult

type Archive = query.Archive

// --- L5 types ---

type CrystalListQuery = query.CrystalListQuery

type CrystalListResult = query.CrystalListResult

type CrystalSummary = query.CrystalSummary

// --- Import / Store types ---

type ImportRequest = query.ImportRequest

type ImportResult = query.ImportResult

type StoreBatch = query.StoreBatch

type StoreItem = query.StoreItem

type StoreResult = query.StoreResult

// --- Update types ---

type UpdateRequest = query.UpdateRequest

type UpdateResult = query.UpdateResult

type UpdateL2Fields = query.UpdateL2Fields

type UpdateL3Fields = query.UpdateL3Fields

type UpdateL5Fields = query.UpdateL5Fields

// --- Profile types ---

type ProfileDelta = query.ProfileDelta

// --- Health types ---

type HealthStatus = query.HealthStatus

type SessionStatus = query.SessionStatus

type MemHopStats = query.MemHopStats

// --- Model types (minimal exposure) ---

type ProfileSlot = model.ProfileSlot

type HypergraphNode = model.HypergraphNode

type HypergraphEdge = model.HypergraphEdge

type HypergraphSlot = model.HypergraphSlot

type HypergraphSource = model.HypergraphSource

type SourceKind = model.SourceKind

var SourceManual = model.SourceManual

type GraphEdgeKind = model.GraphEdgeKind

var (
	EdgeRelated    = model.EdgeRelated
	EdgeCausal     = model.EdgeCausal
	EdgePartOf     = model.EdgePartOf
	EdgeSequence   = model.EdgeSequence
	EdgeDependency = model.EdgeDependency
	EdgeCustom     = model.EdgeCustom
)

// --- Dream types ---

type DreamReport = dream.DreamReport

type LlmProvider = dream.LlmProvider

var NewOpenAIProvider = dream.NewOpenAIProvider

// --- DSL types ---

type DSLQueryResult = dsl.QueryResult

// --- Knowledge types ---

type KnowledgeListQuery = query.KnowledgeListQuery

type KnowledgeListResult = query.KnowledgeListResult

type KnowledgeSummary = query.KnowledgeSummary

type KnowledgeDetail = query.KnowledgeDetail

type KnowledgeNodeQuery = query.KnowledgeNodeQuery

type KnowledgeNodesResult = query.KnowledgeNodesResult

type KnowledgeNodeDetail = query.KnowledgeNodeDetail

// --- Import data types ---

type ImportData = query.ImportData

type ProfileImportData = query.ProfileImportData

type TopicImportItem = query.TopicImportItem

type KnowledgeImportItem = query.KnowledgeImportItem

type ImportError = query.ImportError

type RequestSource = query.RequestSource

// --- Import target layers / modes / status ---

type TargetLayer = query.TargetLayer

type ImportMode = query.ImportMode

type ImportStatus = query.ImportStatus

var (
	TargetProfile   = query.TargetProfile
	TargetTopic     = query.TargetTopic
	TargetKnowledge = query.TargetKnowledge

	ImportMerge     = query.ImportMerge
	ImportOverwrite = query.ImportOverwrite
	ImportSkip      = query.ImportSkip

	ImportSuccess = query.ImportSuccess
)

// --- Update action types ---

type ActionItem = query.ActionItem

// --- Encoder types (for OpenWithEncoder callers) ---

type Encoder = encoder.Encoder

type EncoderOutput = encoder.EncoderOutput

// --- Hash helpers (for external test / SDK use) ---

var (
	HashID     = hash.HashID
	FormatHash = hash.FormatHash
	ParseID    = hash.ParseID
)
