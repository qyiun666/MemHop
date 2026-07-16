// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package storage

import (
	"errors"
	"io"
	"os"
	"sync"

	"github.com/qyiun666/memhop/memhop/internal/core"
)

// RecordEntry is used for batch writes.
type RecordEntry struct {
	RecordType uint8
	IDHash     uint64
	Data       []byte
}

// StorageEngine is a V2 append-only storage engine with A/B dual headers.
type StorageEngine struct {
	file         *os.File
	mmap         []byte
	headerA      *FileHeader
	headerB      *FileHeader
	activeHeader uint8 // 0 = A, 1 = B
	index        map[uint64]uint64
	recordCount  uint32
	nextOffset   uint64
	snapshotData *IndexSnapshotData
	dirty        bool // records written/deleted since the last checkpoint
	mu           sync.RWMutex
}

// Create creates a new .meh file at the given path.
func Create(path string, vectorDim uint16) (*StorageEngine, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return nil, core.NewError(core.ErrIO, "create file", err)
	}
	if err := f.Truncate(DataStart); err != nil {
		f.Close()
		return nil, core.NewError(core.ErrIO, "truncate", err)
	}
	hdr := NewFileHeader(vectorDim)
	hdrBytes := hdr.ToBytes()
	if err := writeHeaderAt(f, HeaderAOffset, hdrBytes); err != nil {
		f.Close()
		return nil, err
	}
	if err := writeHeaderAt(f, HeaderBOffset, hdrBytes); err != nil {
		f.Close()
		return nil, err
	}
	mm, err := MapFile(f, DataStart)
	if err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		UnmapFile(mm)
		f.Close()
		return nil, core.NewError(core.ErrIO, "sync", err)
	}
	return &StorageEngine{
		file:         f,
		mmap:         mm,
		headerA:      hdr,
		headerB:      copyHeader(hdr),
		activeHeader: 0,
		index:        make(map[uint64]uint64),
		nextOffset:   DataStart,
	}, nil
}

// Open opens an existing .meh file.
func Open(path string) (*StorageEngine, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return nil, core.NewError(core.ErrIO, "open file", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, core.NewError(core.ErrIO, "stat", err)
	}
	if info.Size() < HeaderSize*2 {
		f.Close()
		return nil, core.NewError(core.ErrCorruption, "file too small for dual headers")
	}
	mm, err := MapFile(f, int(info.Size()))
	if err != nil {
		f.Close()
		return nil, err
	}
	hA, hB, activeIdx, err := loadHeaders(mm)
	if err != nil {
		UnmapFile(mm)
		f.Close()
		return nil, err
	}
	active := hA
	if activeIdx == 1 {
		active = hB
	}
	e := &StorageEngine{
		file:         f,
		mmap:         mm,
		headerA:      hA,
		headerB:      hB,
		activeHeader: activeIdx,
		index:        make(map[uint64]uint64),
		recordCount:  active.RecordCount,
		nextOffset:   DataStart,
	}
	if active.SnapshotOffset > 0 && active.SnapshotLength > 0 {
		if err := e.loadSnapshot(); err != nil {
			UnmapFile(mm)
			f.Close()
			return nil, err
		}
		// Recover records appended after the snapshot (crash without checkpoint).
		err = e.scanRecords(active.SnapshotOffset + uint64(active.SnapshotLength))
	} else {
		err = e.scanRecords(DataStart)
	}
	if err != nil {
		UnmapFile(mm)
		f.Close()
		return nil, err
	}
	e.recordCount = uint32(len(e.index))
	return e, nil
}

// WriteRecord writes a single record and returns its file offset.
func (e *StorageEngine) WriteRecord(recordType uint8, idHash uint64, data []byte) (uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	offsets, err := e.writeRecordBatch([]RecordEntry{{RecordType: recordType, IDHash: idHash, Data: data}})
	if err != nil {
		return 0, err
	}
	return offsets[0], nil
}

// WriteRecordBatch writes multiple records in a single flush+remap cycle.
func (e *StorageEngine) WriteRecordBatch(records []RecordEntry) ([]uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.writeRecordBatch(records)
}

// ReadRecord reads a record by idHash, returning a copy of the data.
func (e *StorageEngine) ReadRecord(idHash uint64) (uint8, []byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	offset, ok := e.index[idHash]
	if !ok {
		return 0, nil, core.ErrNotFound
	}
	rt, _, data, _, err := RecordData(e.mmap, offset)
	if err != nil {
		return 0, nil, err
	}
	return rt, data, nil
}

// DeleteRecord removes a record by appending a FlagDeleted tombstone (same
// idHash, original record type, empty data) and dropping it from the index.
// The tombstone is synced to disk so the delete survives a crash before the
// next checkpoint; scanRecords replays it as a delete on Open.
func (e *StorageEngine) DeleteRecord(idHash uint64) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	offset, ok := e.index[idHash]
	if !ok {
		return false, nil
	}
	// Preserve the original record type for forensics.
	var rt uint8
	if int(offset) < len(e.mmap) {
		rt = e.mmap[int(offset)]
	}
	tombstone := EncodeRecord(rt, FlagDeleted, idHash, nil)
	end, err := e.file.Seek(0, io.SeekEnd)
	if err != nil {
		return false, core.NewError(core.ErrIO, "seek end", err)
	}
	if _, err := e.file.Write(tombstone); err != nil {
		return false, core.NewError(core.ErrIO, "write tombstone", err)
	}
	if err := e.file.Sync(); err != nil {
		return false, core.NewError(core.ErrIO, "sync tombstone", err)
	}
	delete(e.index, idHash)
	e.recordCount--
	e.nextOffset = uint64(end) + uint64(len(tombstone))
	e.dirty = true
	return true, nil
}

// Contains checks whether idHash exists in the index.
func (e *StorageEngine) Contains(idHash uint64) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, ok := e.index[idHash]
	return ok
}

// Checkpoint persists the index snapshot and switches A/B headers.
func (e *StorageEngine) Checkpoint(snap *IndexSnapshotData) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.checkpoint(snap)
}

// Close checkpoints, syncs, unmaps, and closes the file.
// All cleanup steps execute even if checkpoint fails.
func (e *StorageEngine) Close(snap *IndexSnapshotData) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	ckptErr := e.checkpoint(snap)
	if err := UnmapFile(e.mmap); err != nil {
		return err
	}
	if err := e.file.Sync(); err != nil {
		return core.NewError(core.ErrIO, "sync", err)
	}
	if err := e.file.Close(); err != nil {
		return err
	}
	return ckptErr
}

// CloseNoCheckpoint unmaps and closes the file without writing an index
// snapshot or flipping the A/B header. On-disk state remains exactly as of
// the last checkpoint plus any appended records.
func (e *StorageEngine) CloseNoCheckpoint() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := UnmapFile(e.mmap); err != nil {
		return err
	}
	return e.file.Close()
}

// IterIndex iterates over all (idHash, offset) pairs.
// The index is copied under the read lock first and fn is invoked without
// holding any lock, so fn may safely call engine methods (e.g. ReadRecord);
// a recursive read lock could deadlock once a writer is queued. Iteration
// observes a snapshot: concurrent writes/deletes during iteration are not
// seen. Return false from fn to stop iteration.
func (e *StorageEngine) IterIndex(fn func(idHash, offset uint64) bool) {
	e.mu.RLock()
	pairs := make([]uint64, 0, len(e.index)*2)
	for id, off := range e.index {
		pairs = append(pairs, id, off)
	}
	e.mu.RUnlock()
	for i := 0; i < len(pairs); i += 2 {
		if !fn(pairs[i], pairs[i+1]) {
			return
		}
	}
}

// RecordCount returns the number of live records.
func (e *StorageEngine) RecordCount() uint32 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.recordCount
}

// VectorDim returns the configured vector dimension.
func (e *StorageEngine) VectorDim() uint16 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeHeaderRef().VectorDim
}

// FileSize returns the total mapped file size.
func (e *StorageEngine) FileSize() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return uint64(len(e.mmap))
}

// SnapshotData returns the last loaded snapshot data, if any.
func (e *StorageEngine) SnapshotData() *IndexSnapshotData {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snapshotData
}

// --- internal helpers (must be called with lock held) ---

func (e *StorageEngine) activeHeaderRef() *FileHeader {
	if e.activeHeader == 0 {
		return e.headerA
	}
	return e.headerB
}

func (e *StorageEngine) writeRecordBatch(records []RecordEntry) ([]uint64, error) {
	if len(records) == 0 {
		return nil, nil
	}
	// Append everything to the file first. The in-memory index, recordCount
	// and mmap are updated only after all writes, the sync and the remap
	// have succeeded, so a failure mid-batch leaves the engine state
	// consistent (the stale bytes past nextOffset are ignored on Open).
	offsets := make([]uint64, 0, len(records))
	for _, rec := range records {
		encoded := EncodeRecord(rec.RecordType, 0, rec.IDHash, rec.Data)
		offset, err := e.file.Seek(0, io.SeekEnd)
		if err != nil {
			return nil, core.NewError(core.ErrIO, "seek end", err)
		}
		if _, err := e.file.Write(encoded); err != nil {
			return nil, core.NewError(core.ErrIO, "write record", err)
		}
		offsets = append(offsets, uint64(offset))
	}
	if err := e.file.Sync(); err != nil {
		return nil, core.NewError(core.ErrIO, "sync", err)
	}
	mm, err := RemapFile(e.file, e.mmap)
	if err != nil {
		return nil, err
	}
	e.mmap = mm
	for i, rec := range records {
		if _, exists := e.index[rec.IDHash]; !exists {
			e.recordCount++
		}
		e.index[rec.IDHash] = offsets[i]
	}
	last := records[len(records)-1]
	e.nextOffset = offsets[len(offsets)-1] + uint64(RecordHeaderSize+len(last.Data))
	e.dirty = true
	return offsets, nil
}

func (e *StorageEngine) checkpoint(snap *IndexSnapshotData) error {
	blob, err := BuildSnapshot(e.index, snap)
	if err != nil {
		return err
	}
	snapOffset, err := e.file.Seek(0, io.SeekEnd)
	if err != nil {
		return core.NewError(core.ErrIO, "seek snap", err)
	}
	if _, err := e.file.Write(blob); err != nil {
		return core.NewError(core.ErrIO, "write snapshot", err)
	}
	if err := e.file.Sync(); err != nil {
		return core.NewError(core.ErrIO, "sync", err)
	}
	mm, err := RemapFile(e.file, e.mmap)
	if err != nil {
		return err
	}
	e.mmap = mm
	// Build new header written to inactive slot.
	newHdr := e.buildCheckpointHeader(snapOffset, uint32(len(blob)))
	isA := e.activeHeader == 1
	writeOffset := int64(HeaderAOffset)
	if !isA {
		writeOffset = HeaderBOffset
	}
	if err := writeHeaderAt(e.file, writeOffset, newHdr.ToBytes()); err != nil {
		return err
	}
	if err := e.file.Sync(); err != nil {
		return core.NewError(core.ErrIO, "sync header", err)
	}
	mm, err = RemapFile(e.file, e.mmap)
	if err != nil {
		return err
	}
	e.mmap = mm
	// Switch active.
	if isA {
		e.headerA = newHdr
		e.activeHeader = 0
	} else {
		e.headerB = newHdr
		e.activeHeader = 1
	}
	return nil
}

func (e *StorageEngine) buildCheckpointHeader(snapOffset int64, snapLen uint32) *FileHeader {
	h := copyHeader(e.activeHeaderRef())
	h.CommitID++
	h.SnapshotOffset = uint64(snapOffset)
	h.SnapshotLength = snapLen
	h.RecordCount = e.recordCount
	h.CRC32 = h.calculateCRC()
	return h
}

func (e *StorageEngine) loadSnapshot() error {
	active := e.activeHeaderRef()
	off := int(active.SnapshotOffset)
	length := int(active.SnapshotLength)
	if off+length > len(e.mmap) {
		return core.NewError(core.ErrCorruption, "snapshot out of bounds")
	}
	raw := make([]byte, length)
	copy(raw, e.mmap[off:off+length])
	idx, snap, err := ParseSnapshot(raw)
	if err != nil {
		return err
	}
	e.index = idx
	e.recordCount = uint32(len(idx))
	e.snapshotData = snap
	// Recompute nextOffset from index.
	e.recomputeNextOffset()
	return nil
}

// scanRecords scans records starting at the given offset and merges them
// into the index. A later record with the same idHash overrides an earlier
// entry; records flagged FlagDeleted are skipped. Sets nextOffset just past
// the last valid record.
func (e *StorageEngine) scanRecords(start uint64) error {
	offset := start
	for {
		_, flags, data, idHash, err := RecordData(e.mmap, offset)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if flags&FlagDeleted == 0 {
			e.index[idHash] = offset
		}
		offset += uint64(RecordHeaderSize) + uint64(len(data))
	}
	e.nextOffset = offset
	return nil
}

func (e *StorageEngine) recomputeNextOffset() {
	var maxEnd uint64 = DataStart
	for _, off := range e.index {
		_, _, data, _, err := RecordData(e.mmap, off)
		if err != nil {
			continue
		}
		end := off + uint64(RecordHeaderSize) + uint64(len(data))
		if end > maxEnd {
			maxEnd = end
		}
	}
	e.nextOffset = maxEnd
}

// --- package-level helpers ---

func writeHeaderAt(f *os.File, offset int64, buf [HeaderSize]byte) error {
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return core.NewError(core.ErrIO, "seek header", err)
	}
	if _, err := f.Write(buf[:]); err != nil {
		return core.NewError(core.ErrIO, "write header", err)
	}
	return nil
}

func loadHeaders(mm []byte) (hA, hB *FileHeader, activeIdx uint8, err error) {
	var bufA, bufB [HeaderSize]byte
	copy(bufA[:], mm[:HeaderSize])
	copy(bufB[:], mm[HeaderSize:HeaderSize*2])
	hA, err = FileHeaderFromBytes(bufA)
	if err != nil {
		return nil, nil, 0, err
	}
	hB, err = FileHeaderFromBytes(bufB)
	if err != nil {
		return nil, nil, 0, err
	}
	active, err := SelectValidHeader(hA, hB)
	if err != nil {
		return nil, nil, 0, err
	}
	if active.CommitID == hA.CommitID {
		return hA, hB, 0, nil
	}
	return hA, hB, 1, nil
}

func copyHeader(h *FileHeader) *FileHeader {
	c := *h
	return &c
}
