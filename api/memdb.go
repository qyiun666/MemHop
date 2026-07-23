// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package memhop provides the unified public API facade for the MemHop memory engine.
// It assembles all underlying components (storage, index, query, dream, encoder)
// into a single MemHop struct with thread-safe methods.
//
// This file contains:
//   - Lifecycle: Open, OpenWithEncoder, Close, Checkpoint
//   - Health: HealthCheck, SessionStatus
//   - Public type aliases, sentinel errors, constants, and variables
package memhop

import (
	"log/slog"
	"os"
	"sync/atomic"

	"memhop/internal/common/config"
	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
	"memhop/internal/core/index"
	"memhop/internal/core/model"
	"memhop/internal/core/storage"
	"memhop/internal/query/crud"
	"memhop/internal/query/dream"
	"memhop/internal/query/encoder"
	l3 "memhop/internal/query/graph"
	"memhop/internal/query/graph/dsl"
	"memhop/internal/query/health"
	"memhop/internal/query/importx"
	"memhop/internal/query/search"
	"memhop/internal/query/session"
	"memhop/internal/query/write"
)

// Lock order (to prevent deadlock): storage → l2meta → sparse → l1reverse → l3index → l3cache
//
// l2Meta / l1Reverse are held as atomic.Pointer so Dream() can atomically
// swap them at the end of a consolidation cycle without blocking readers.
// The pointed-to index instances remain immutable snapshots between swaps;
// concurrent mutation of a loaded snapshot is out of scope for this lock.

// MemHop is the main database instance.
type MemHop struct {
	engine       *storage.StorageEngine
	config       *config.MemHopConfig
	defaults     *config.MemHopDefaults
	sparseIndex  *index.SparseIndex
	l2Meta       atomic.Pointer[index.L2MetaIndex]
	l1Reverse    atomic.Pointer[index.L1ReverseIndex]
	sessionMgr   *session.SessionManager
	encoder      encoder.Encoder
	l3Index      *index.L3Index
	l3Cache      *index.AdjacencyCache
	l3Degree     *index.DegreeTracker
	profileCache *search.ProfileResult // L0 profile cache (nil = not cached)
	closed       atomic.Bool
}

// getL2Meta returns the currently active L2 metadata index snapshot.
func (m *MemHop) getL2Meta() *index.L2MetaIndex { return m.l2Meta.Load() }

// getL1Reverse returns the currently active L1 reverse index snapshot.
func (m *MemHop) getL1Reverse() *index.L1ReverseIndex { return m.l1Reverse.Load() }

// Open creates or opens a MemHop database.
// Config.EncoderAddr and Config.EmbedModel are required.
func Open(config *config.MemHopConfig) (*MemHop, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	enc, err := createEncoder(config)
	if err != nil {
		return nil, err
	}
	return openWithEncoder(config, enc)
}

// OpenWithEncoder creates or opens a MemHop database with a custom encoder.
func OpenWithEncoder(config *config.MemHopConfig, enc encoder.Encoder) (*MemHop, error) {
	return openWithEncoder(config, enc)
}

// Close persists all data and releases resources.
func (m *MemHop) Close() error {
	if !m.closed.CompareAndSwap(false, true) {
		return mherrors.ErrClosed
	}
	snap, err := m.buildSnapshot()
	if err != nil {
		return err
	}
	var encErr error
	if c, ok := m.encoder.(interface{ Close() error }); ok {
		encErr = c.Close()
	}
	// Always close engine to release mmap/file even if encoder failed.
	engErr := m.engine.Close(snap)
	if encErr != nil {
		return mherrors.NewError(mherrors.ErrEncoder, "encoder close", encErr)
	}
	return engErr
}

// Checkpoint persists current state to disk without closing.
func (m *MemHop) Checkpoint() error {
	if m.closed.Load() {
		return mherrors.ErrClosed
	}
	snap, err := m.buildSnapshot()
	if err != nil {
		return err
	}
	return m.engine.Checkpoint(snap)
}

// HealthCheck returns the current health status of the database.
func (m *MemHop) HealthCheck() (*health.HealthStatus, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	layerCounts := health.CountLayers(m.engine)
	issues := health.CollectIssues(m.encoder, layerCounts)
	return &health.HealthStatus{
		OK:                len(issues) == 0,
		DBSizeBytes:       m.engine.FileSize(),
		LayerCounts:       layerCounts,
		EncoderConfigured: m.encoder != nil && m.encoder.IsAvailable(),
		Issues:            issues,
	}, nil
}

// SessionStatus returns the current session activation state.
func (m *MemHop) SessionStatus() (*crud.SessionStatus, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	rawIDs := m.sessionMgr.GetActiveTopicIDs()
	hexIDs := make([]string, len(rawIDs))
	for i, id := range rawIDs {
		hexIDs[i] = hash.FormatHash(id)
	}
	return &crud.SessionStatus{
		ActiveTopicIDs: hexIDs,
		Count:          len(hexIDs),
		IsEmpty:        len(hexIDs) == 0,
	}, nil
}

// --- internal helpers ---

func (m *MemHop) buildSnapshot() (*storage.IndexSnapshotData, error) {
	sparseData, err := m.sparseIndex.Serialize()
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrSerialization, "sparse index", err)
	}
	l1RevData, err := m.getL1Reverse().Serialize()
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrSerialization, "l1 reverse index", err)
	}
	// L3IndexData is intentionally left nil: L3 hypergraph data (graph slots,
	// nodes, edges) is persisted directly as individual records in the storage
	// engine and does not require a separate in-memory index snapshot.
	return &storage.IndexSnapshotData{
		SparseData:    sparseData,
		L1ReverseData: l1RevData,
		L3IndexData:   nil,
	}, nil
}

func (m *MemHop) searchDeps() *search.SearchDeps {
	return &search.SearchDeps{
		SparseIndex:  m.sparseIndex,
		L2Meta:       m.getL2Meta(),
		VectorDim:    m.config.VectorDim,
		Engine:       m.engine,
		Encoder:      m.encoder,
		Weights:      m.defaults.SearchWeights,
		L1Reverse:    m.getL1Reverse(),
		ProfileCache: &m.profileCache,
	}
}

func (m *MemHop) batchDeps() *write.BatchDeps {
	return &write.BatchDeps{
		Engine:      m.engine,
		SparseIndex: m.sparseIndex,
		VectorDim:   m.config.VectorDim,
		Encoder:     m.encoder,
	}
}

// --- package-level initialization helpers ---

func openWithEncoder(config *config.MemHopConfig, enc encoder.Encoder) (*MemHop, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	dflt := config.Defaults
	if dflt == nil {
		dflt = DefaultDefaults()
	}

	// Initialize the global tokenizer.
	tok := dflt.TokenizerEngine
	if tok == "" {
		slog.Warn("TokenizerEngine not set, defaulting to auto")
		tok = "auto"
	}
	if err := index.InitTokenizer(tok); err != nil {
		return nil, mherrors.NewError(mherrors.ErrConfig, "tokenizer init failed", err)
	}

	engine, err := openOrCreateEngine(config)
	if err != nil {
		return nil, err
	}
	if int(engine.VectorDim()) != config.VectorDim {
		// Close without checkpointing: writing an (empty) snapshot here would
		// flip the A/B header and destroy the on-disk index snapshot.
		engine.CloseNoCheckpoint()
		return nil, mherrors.NewError(mherrors.ErrVectorDimMismatch, "config vs engine")
	}
	sparseIdx, l1Rev := loadCachedIndices(engine)
	l2MetaIdx := index.BuildL2MetaFromEngine(engine)
	sm := session.NewSessionManager(dflt.SessionConfig)
	l3Idx := index.NewL3Index()
	if err := l3Idx.BuildFromEngine(engine); err != nil {
		slog.Warn("l3 index build failed", "error", err)
	}
	l3CacheMax := dflt.AdjacencyCacheMaxEntries
	if l3CacheMax <= 0 {
		slog.Warn("AdjacencyCacheMaxEntries not set or <= 0, defaulting to 128")
		l3CacheMax = 128
	}
	l3C := index.NewAdjacencyCache(l3CacheMax)
	l3Dt := index.NewDegreeTracker()
	m := &MemHop{
		engine: engine, config: config, defaults: dflt,
		sparseIndex: sparseIdx,
		sessionMgr:  sm, encoder: enc,
		l3Index: l3Idx, l3Cache: l3C, l3Degree: l3Dt,
	}
	m.l2Meta.Store(l2MetaIdx)
	m.l1Reverse.Store(l1Rev)
	return m, nil
}

func createEncoder(config *config.MemHopConfig) (encoder.Encoder, error) {
	if config.EncoderAddr == "" {
		return nil, mherrors.NewError(mherrors.ErrConfig,
			"Config.EncoderAddr is required")
	}
	if config.EmbedModel == "" {
		return nil, mherrors.NewError(mherrors.ErrConfig,
			"Config.EmbedModel is required")
	}
	if config.LLM.APIURL == "" || config.LLM.APIKey == "" {
		return nil, mherrors.NewError(mherrors.ErrConfig,
			"LLM.APIURL and LLM.APIKey are required")
	}
	enc, err := encoder.NewHttpEncoder(config.EncoderAddr, config.VectorDim, config.EmbedModel)
	if err != nil {
		return nil, err
	}
	return enc, nil
}

func openOrCreateEngine(config *config.MemHopConfig) (*storage.StorageEngine, error) {
	if _, err := os.Stat(config.DBPath); err == nil {
		return storage.Open(config.DBPath)
	}
	return storage.Create(config.DBPath, uint16(config.VectorDim))
}

func loadCachedIndices(engine *storage.StorageEngine) (*index.SparseIndex, *index.L1ReverseIndex) {
	sparseIdx := index.NewSparseIndex()
	l1Rev := index.NewL1ReverseIndex()
	snap := engine.SnapshotData()
	if snap == nil {
		return sparseIdx, l1Rev
	}
	if len(snap.SparseData) > 0 {
		if idx, err := index.DeserializeSparseIndex(snap.SparseData); err == nil {
			sparseIdx = idx
		} else {
			slog.Warn("sparse index snapshot deserialize failed, rebuilding empty", "error", err)
		}
	}
	if len(snap.L1ReverseData) > 0 {
		if idx, err := index.DeserializeL1ReverseIndex(snap.L1ReverseData); err == nil {
			l1Rev = idx
		} else {
			slog.Warn("l1 reverse index snapshot deserialize failed, rebuilding empty", "error", err)
		}
	}
	return sparseIdx, l1Rev
}

// ---------------------------------------------------------------------------
// Public type aliases, sentinel errors, constants, and variables
// ---------------------------------------------------------------------------

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
