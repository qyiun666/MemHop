// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package memhop is the public facade for the MemHop memory database.
// It re-exports only the types and functions needed by external consumers.
// All internal implementation lives in memhop/internal/query subpackages.
package memhop

import (
	"memhop/internal/common/config"
	"memhop/internal/query/dream"
	"memhop/internal/query/encoder"
	l3 "memhop/internal/query/graph"
	"memhop/internal/query/graph/dsl"
	"memhop/internal/core/model"
	"memhop/internal/query/search"
	"memhop/internal/query/write"
	"memhop/internal/query/crud"
	"memhop/internal/query/importx"
	"memhop/internal/query/health"
	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
)

// --- Config types ---

type Config = config.MemHopConfig

type ConfigDefaults = config.MemHopDefaults

var DefaultDefaults = config.DefaultMemHopDefaults

// --- Error types ---

var (
	ErrIO                = mherrors.ErrIO
	ErrInvalidMagic      = mherrors.ErrInvalidMagic
	ErrCRCMismatch       = mherrors.ErrCRCMismatch
	ErrCorruption        = mherrors.ErrCorruption
	ErrNotFound          = mherrors.ErrNotFound
	ErrVectorDimMismatch = mherrors.ErrVectorDimMismatch
	ErrSerialization     = mherrors.ErrSerialization
	ErrDeserialization   = mherrors.ErrDeserialization
	ErrEncoder           = mherrors.ErrEncoder
	ErrConfig            = mherrors.ErrConfig
	ErrLLM               = mherrors.ErrLLM
	ErrInvalidQuery      = mherrors.ErrInvalidQuery
	ErrClosed            = mherrors.ErrClosed
)

type Error = mherrors.MemHopError

var NewError = mherrors.NewError

// --- Search types ---

type SearchQuery = search.SearchQuery

type SearchResult = search.SearchResult

type SearchDefaults = search.SearchDefaults

type ContextResult = search.ContextResult

type ProfileResult = search.ProfileResult

type L1Preview = search.L1Preview

type L3Preview = search.L3Preview

type L3SearchQuery = crud.L3SearchQuery

type L3SearchResult = crud.L3SearchResult

type L3EntityHint = dream.L3EntityHint

type SearchPreprocessResult = dream.SearchPreprocessResult

type RequestSource = search.RequestSource

// --- CRUD types ---

type TopicListQuery = crud.TopicListQuery

type TopicListResult = crud.TopicListResult

type TopicSummary = crud.TopicSummary

type TopicDetail = crud.TopicDetail

type L3Detail = crud.L3Detail

type GraphNode = crud.GraphNode

type GraphEdge = crud.GraphEdge

type Subgraph = crud.Subgraph

type TraversalHop = crud.TraversalHop

type MergeResult = crud.MergeResult

type SceneTreeResult = crud.SceneTreeResult

type L1Graph = crud.L1Graph

type L1Node = crud.L1Node

type L1Edge = crud.L1Edge

// --- L3 types ---

type CommunityConfig = l3.CommunityConfig

type CommunityResult = l3.CommunityResult

var DefaultCommunityConfig = l3.DefaultCommunityConfig

// --- Archive types ---

type ArchiveQuery = crud.ArchiveQuery

type ArchiveListResult = crud.ArchiveListResult

type Archive = crud.Archive

// --- L5 types ---

type CrystalListQuery = crud.CrystalListQuery

type CrystalListResult = crud.CrystalListResult

type CrystalSummary = crud.CrystalSummary

// --- Import / Store types ---

type ImportRequest = importx.ImportRequest

type ImportResult = importx.ImportResult

type StoreBatch = write.StoreBatch

type StoreItem = write.StoreItem

type StoreResult = write.StoreResult

// --- Update types ---

type UpdateRequest = crud.UpdateRequest

type UpdateResult = crud.UpdateResult

type UpdateL2Fields = crud.UpdateL2Fields

type UpdateL3Fields = crud.UpdateL3Fields

type UpdateL5Fields = crud.UpdateL5Fields

// --- L5 write types ---

type L5ChainInput = crud.L5ChainInput

type L5StepInput = crud.L5StepInput

type L5ChainUpdate = crud.L5ChainUpdate

// --- Profile types ---

type ProfileDelta = crud.ProfileDelta

// --- Health types ---

type HealthStatus = health.HealthStatus

type SessionStatus = crud.SessionStatus

type MemHopStats = health.MemHopStats

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

type KnowledgeListQuery = crud.KnowledgeListQuery

type KnowledgeListResult = crud.KnowledgeListResult

type KnowledgeSummary = crud.KnowledgeSummary

type KnowledgeDetail = crud.KnowledgeDetail

type KnowledgeNodeQuery = crud.KnowledgeNodeQuery

type KnowledgeNodesResult = crud.KnowledgeNodesResult

type KnowledgeNodeDetail = crud.KnowledgeNodeDetail

// --- Import data types ---

type ImportData = importx.ImportData

type ProfileImportData = importx.ProfileImportData

type TopicImportItem = importx.TopicImportItem

type KnowledgeImportItem = importx.KnowledgeImportItem

type ImportError = write.ImportError

// --- Import target layers / modes / status ---

type TargetLayer = write.TargetLayer

type ImportMode = write.ImportMode

type ImportStatus = write.ImportStatus

var (
	TargetProfile   = write.TargetProfile
	TargetTopic     = write.TargetTopic
	TargetKnowledge = write.TargetKnowledge

	ImportMerge     = write.ImportMerge
	ImportOverwrite = write.ImportOverwrite
	ImportSkip      = write.ImportSkip

	ImportSuccess = write.ImportSuccess
)

// --- Update action types ---

type ActionItem = write.ActionItem

// --- Encoder types (for OpenWithEncoder callers) ---

type Encoder = encoder.Encoder

type EncoderOutput = encoder.EncoderOutput

// --- Hash helpers (for external test / SDK use) ---

var (
	HashID     = hash.HashID
	FormatHash = hash.FormatHash
	ParseID    = hash.ParseID
)
