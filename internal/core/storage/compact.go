// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package storage

import "github.com/qyiun666/MemHop/internal/common/mherrors"

// Compact creates a new file at newPath containing only live records.
// snap must carry the caller's current serialized indices: compacting with
// an empty snapshot would silently drop the sparse/L1/L3 index data.
func (e *StorageEngine) Compact(newPath string, snap *IndexSnapshotData) error {
	if snap == nil {
		return mherrors.NewError(mherrors.ErrInvalidQuery, "compact requires an index snapshot")
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	newEng, err := Create(newPath, e.activeHeaderRef().VectorDim)
	if err != nil {
		return err
	}
	needsCleanup := true
	defer func() {
		if needsCleanup {
			UnmapFile(newEng.mmap)
			unlockFile(newEng.file)
			newEng.file.Close()
		}
	}()
	for idHash, offset := range e.index {
		rt, _, data, _, readErr := RecordData(e.mmap, offset)
		if readErr != nil {
			return mherrors.NewError(mherrors.ErrCorruption, "compact: read live record", readErr)
		}
		if _, writeErr := newEng.WriteRecord(rt, idHash, data); writeErr != nil {
			return writeErr
		}
	}
	if err := newEng.Checkpoint(snap); err != nil {
		return err
	}
	// Checkpoint synced data; release fd, lock and mmap without writing another snapshot.
	needsCleanup = false
	UnmapFile(newEng.mmap)
	if err := unlockFile(newEng.file); err != nil {
		return err
	}
	return newEng.file.Close()
}
