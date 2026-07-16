// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package storage

// Compact creates a new file at newPath containing only live records.
func (e *StorageEngine) Compact(newPath string) error {
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
			newEng.file.Close()
		}
	}()
	for idHash, offset := range e.index {
		rt, _, data, _, readErr := RecordData(e.mmap, offset)
		if readErr != nil || data == nil {
			continue
		}
		if _, writeErr := newEng.WriteRecord(rt, idHash, data); writeErr != nil {
			return writeErr
		}
	}
	if err := newEng.Checkpoint(&IndexSnapshotData{}); err != nil {
		return err
	}
	// Checkpoint synced data; release fd and mmap without writing another snapshot.
	needsCleanup = false
	UnmapFile(newEng.mmap)
	return newEng.file.Close()
}
