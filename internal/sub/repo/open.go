// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package repo exposes the storage engine and index layer to outer packages.
// core/ may only be referenced from inside this package (and its subpackages);
// outer packages must call the methods defined here instead of importing
// internal/sub/repo/core directly.
package repo

import (
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
	coreindex "github.com/qyiun666/MemHop/internal/sub/repo/index"
)

// --- Type aliases (core types re-exported without exposing core/) ---

type StorageEngine = core.StorageEngine
type IndexSnapshotData = core.IndexSnapshotData
type SparseIndex = coreindex.SparseIndex
type L1ReverseIndex = coreindex.L1ReverseIndex

// --- Engine lifecycle ---

// Open opens an existing .meh file.
func Open(path string) (*StorageEngine, error) {
	return core.Open(path)
}

// Create creates a new .meh file at the given path.
func Create(path string, vectorDim uint16) (*StorageEngine, error) {
	return core.Create(path, vectorDim)
}

// Close checkpoints, syncs, unmaps, and closes the engine.
func Close(engine *StorageEngine, snap *IndexSnapshotData) error {
	return engine.Close(snap)
}

// CloseNoCheckpoint unmaps and closes the engine without writing a snapshot.
func CloseNoCheckpoint(engine *StorageEngine) error {
	return engine.CloseNoCheckpoint()
}

// Checkpoint persists the index snapshot and switches A/B headers.
func Checkpoint(engine *StorageEngine, snap *IndexSnapshotData) error {
	return engine.Checkpoint(snap)
}

// CheckpointReclaim 回收式检查点：删除全部旧快照仅保留最新一份，返回本次
// 写入的快照数据，调用方可直接覆盖其内存快照副本。
func CheckpointReclaim(engine *StorageEngine, snap *IndexSnapshotData) (*IndexSnapshotData, error) {
	return engine.CheckpointReclaim(snap)
}

// VectorDim returns the configured vector dimension.
func VectorDim(engine *StorageEngine) uint16 {
	return engine.VectorDim()
}

// SnapshotData returns the last loaded snapshot data, if any.
func SnapshotData(engine *StorageEngine) *IndexSnapshotData {
	return engine.SnapshotData()
}

// FileSize returns the total mapped file size.
func FileSize(engine *StorageEngine) uint64 {
	return engine.FileSize()
}

// --- Tokenizer ---

// InitTokenizer initializes the global tokenizer (process-wide singleton).
func InitTokenizer(engine string) error {
	return coreindex.InitTokenizer(engine)
}

// --- Sparse / L1 index ---

func NewSparseIndex() *SparseIndex {
	return coreindex.NewSparseIndex()
}

func NewL1ReverseIndex() *L1ReverseIndex {
	return coreindex.NewL1ReverseIndex()
}

func DeserializeSparseIndex(data []byte) (*SparseIndex, error) {
	return coreindex.DeserializeSparseIndex(data)
}

func DeserializeL1ReverseIndex(data []byte) (*L1ReverseIndex, error) {
	return coreindex.DeserializeL1ReverseIndex(data)
}

func SerializeSparseIndex(idx *SparseIndex) ([]byte, error) {
	return idx.Serialize()
}

func SerializeL1ReverseIndex(idx *L1ReverseIndex) ([]byte, error) {
	return idx.Serialize()
}
