// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package repo exposes the storage engine and index layer to outer
// packages. core/ must not be imported outside this package; outer
// packages use the methods defined here.
package repo

import (
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
	coreindex "github.com/qyiun666/MemHop/internal/sub/repo/index"
)

type StorageEngine = core.StorageEngine
type IndexSnapshotData = core.IndexSnapshotData
type SparseIndex = coreindex.SparseIndex
type L1ReverseIndex = coreindex.L1ReverseIndex

func Open(path string) (*StorageEngine, error) {
	return core.Open(path)
}

func Create(path string, vectorDim uint16) (*StorageEngine, error) {
	return core.Create(path, vectorDim)
}

func Close(engine *StorageEngine, snap *IndexSnapshotData) error {
	return engine.Close(snap)
}

func CloseNoCheckpoint(engine *StorageEngine) error {
	return engine.CloseNoCheckpoint()
}

func Checkpoint(engine *StorageEngine, snap *IndexSnapshotData) error {
	return engine.Checkpoint(snap)
}

// CheckpointReclaim deletes all old snapshots keeping only the latest;
// returns the written snapshot data.
func CheckpointReclaim(engine *StorageEngine, snap *IndexSnapshotData) (*IndexSnapshotData, error) {
	return engine.CheckpointReclaim(snap)
}

func VectorDim(engine *StorageEngine) uint16 {
	return engine.VectorDim()
}

func SnapshotData(engine *StorageEngine) *IndexSnapshotData {
	return engine.SnapshotData()
}

func FileSize(engine *StorageEngine) uint64 {
	return engine.FileSize()
}

func InitTokenizer(engine string) error {
	return coreindex.InitTokenizer(engine)
}

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
