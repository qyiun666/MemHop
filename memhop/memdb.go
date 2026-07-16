// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package memhop provides the unified public API facade for the MemHop memory engine.
// It assembles all underlying components (storage, index, query, dream, encoder)
// into a single MemHop struct with thread-safe methods.
package memhop

import (
	"log/slog"
	"os"
	"sync"

	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/encoder"
	"github.com/qyiun666/memhop/memhop/internal/core/index"
	"github.com/qyiun666/memhop/memhop/internal/core/l3"
	"github.com/qyiun666/memhop/memhop/internal/core/query"
	"github.com/qyiun666/memhop/memhop/internal/core/session"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
)

// MemHop is the main database instance.
type MemHop struct {
	engine      *storage.StorageEngine
	config      *core.MemHopConfig
	defaults    *core.MemHopDefaults
	sparseIndex *index.SparseIndex
	l2Meta      *index.L2MetaIndex
	l1Reverse   *query.L1ReverseIndex
	sessionMgr  *session.SessionManager
	encoder     encoder.Encoder
	l3Index     *l3.L3Index
	l3Cache     *l3.AdjacencyCache
	l3Degree    *l3.DegreeTracker
	closed      bool
	mu          sync.RWMutex
}

// Open creates or opens a MemHop database.
// Config.EncoderAddr and Config.EmbedModel are required.
func Open(config *core.MemHopConfig) (*MemHop, error) {
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
func OpenWithEncoder(config *core.MemHopConfig, enc encoder.Encoder) (*MemHop, error) {
	return openWithEncoder(config, enc)
}

// Close persists all data and releases resources.
func (m *MemHop) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return core.ErrClosed
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
	m.closed = true
	if encErr != nil {
		return core.NewError(core.ErrEncoder, "encoder close", encErr)
	}
	return engErr
}

// Checkpoint persists current state to disk without closing.
func (m *MemHop) Checkpoint() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return core.ErrClosed
	}
	snap, err := m.buildSnapshot()
	if err != nil {
		return err
	}
	return m.engine.Checkpoint(snap)
}

// --- internal helpers ---

func (m *MemHop) buildSnapshot() (*storage.IndexSnapshotData, error) {
	sparseData, err := m.sparseIndex.Serialize()
	if err != nil {
		return nil, core.NewError(core.ErrSerialization, "sparse index", err)
	}
	l1RevData, err := m.l1Reverse.Serialize()
	if err != nil {
		return nil, core.NewError(core.ErrSerialization, "l1 reverse index", err)
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

func (m *MemHop) searchDeps() *query.SearchDeps {
	return &query.SearchDeps{
		SparseIndex: m.sparseIndex,
		L2Meta:      m.l2Meta,
		VectorDim:   m.config.VectorDim,
		Engine:      m.engine,
		Encoder:     m.encoder,
		Weights:     m.defaults.SearchWeights,
		L1Reverse:   m.l1Reverse,
	}
}

func (m *MemHop) batchDeps() *query.BatchDeps {
	return &query.BatchDeps{
		Engine:      m.engine,
		SparseIndex: m.sparseIndex,
		VectorDim:   m.config.VectorDim,
		Encoder:     m.encoder,
	}
}

// --- package-level initialization helpers ---

func openWithEncoder(config *core.MemHopConfig, enc encoder.Encoder) (*MemHop, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	dflt := config.Defaults
	if dflt == nil {
		dflt = core.DefaultMemHopDefaults()
	}

	// Initialize the global tokenizer.
	tok := dflt.TokenizerEngine
	if tok == "" {
		slog.Warn("TokenizerEngine not set, defaulting to auto")
		tok = "auto"
	}
	if err := index.InitTokenizer(tok); err != nil {
		return nil, core.NewError(core.ErrConfig, "tokenizer init failed", err)
	}

	engine, err := openOrCreateEngine(config)
	if err != nil {
		return nil, err
	}
	if int(engine.VectorDim()) != config.VectorDim {
		// Close without checkpointing: writing an (empty) snapshot here would
		// flip the A/B header and destroy the on-disk index snapshot.
		engine.CloseNoCheckpoint()
		return nil, core.NewError(core.ErrVectorDimMismatch, "config vs engine")
	}
	sparseIdx, l1Rev := loadCachedIndices(engine)
	l2MetaIdx := index.BuildL2MetaFromEngine(engine)
	sm := session.NewSessionManager(dflt.SessionConfig)
	l3Idx := l3.NewL3Index()
	if err := l3Idx.BuildFromEngine(engine); err != nil {
		slog.Warn("l3 index build failed", "error", err)
	}
	l3CacheMax := dflt.AdjacencyCacheMaxEntries
	if l3CacheMax <= 0 {
		slog.Warn("AdjacencyCacheMaxEntries not set or <= 0, defaulting to 128")
		l3CacheMax = 128
	}
	l3C := l3.NewAdjacencyCache(l3CacheMax)
	l3Dt := l3.NewDegreeTracker()
	return &MemHop{
		engine: engine, config: config, defaults: dflt,
		sparseIndex: sparseIdx, l2Meta: l2MetaIdx,
		l1Reverse:  l1Rev,
		sessionMgr: sm, encoder: enc,
		l3Index: l3Idx, l3Cache: l3C, l3Degree: l3Dt,
	}, nil
}

func createEncoder(config *core.MemHopConfig) (encoder.Encoder, error) {
	if config.EncoderAddr == "" {
		return nil, core.NewError(core.ErrConfig,
			"Config.EncoderAddr is required")
	}
	if config.EmbedModel == "" {
		return nil, core.NewError(core.ErrConfig,
			"Config.EmbedModel is required")
	}
	if config.LLM.APIURL == "" || config.LLM.APIKey == "" {
		return nil, core.NewError(core.ErrConfig,
			"LLM.APIURL and LLM.APIKey are required")
	}
	enc, err := encoder.NewHttpEncoder(config.EncoderAddr, config.VectorDim, config.EmbedModel)
	if err != nil {
		return nil, err
	}
	return enc, nil
}

func openOrCreateEngine(config *core.MemHopConfig) (*storage.StorageEngine, error) {
	if _, err := os.Stat(config.DBPath); err == nil {
		return storage.Open(config.DBPath)
	}
	return storage.Create(config.DBPath, uint16(config.VectorDim))
}

func loadCachedIndices(engine *storage.StorageEngine) (*index.SparseIndex, *query.L1ReverseIndex) {
	sparseIdx := index.NewSparseIndex()
	l1Rev := query.NewL1ReverseIndex()
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
		if idx, err := query.DeserializeL1ReverseIndex(snap.L1ReverseData); err == nil {
			l1Rev = idx
		} else {
			slog.Warn("l1 reverse index snapshot deserialize failed, rebuilding empty", "error", err)
		}
	}
	return sparseIdx, l1Rev
}
