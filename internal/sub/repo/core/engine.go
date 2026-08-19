// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"errors"
	"io"
	"os"
	"sync"

	"github.com/qyiun666/MemHop/internal/sub/common"
)

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
	byType       map[uint8]map[uint64]struct{} // recordType → set of idHashes
	recordCount  uint32
	nextOffset   uint64
	snapshotData *IndexSnapshotData
	dirty        bool // records written/deleted since the last checkpoint
	closed       bool // Close called; all operations return ErrClosed
	mu           sync.RWMutex
}

func Create(path string, vectorDim uint16) (*StorageEngine, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return nil, common.NewError(common.ErrIO, "create file", err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Truncate(DataStart); err != nil {
		f.Close()
		return nil, common.NewError(common.ErrIO, "truncate", err)
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
		return nil, common.NewError(common.ErrIO, "sync", err)
	}
	return &StorageEngine{
		file:         f,
		mmap:         mm,
		headerA:      hdr,
		headerB:      copyHeader(hdr),
		activeHeader: 0,
		index:        make(map[uint64]uint64),
		byType:       make(map[uint8]map[uint64]struct{}),
		nextOffset:   DataStart,
	}, nil
}

func Open(path string) (*StorageEngine, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return nil, common.NewError(common.ErrIO, "open file", err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, common.NewError(common.ErrIO, "stat", err)
	}
	if info.Size() < HeaderSize*2 {
		f.Close()
		return nil, common.NewError(common.ErrCorruption, "file too small for dual headers")
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
		byType:       make(map[uint8]map[uint64]struct{}),
		recordCount:  active.RecordCount,
		nextOffset:   DataStart,
	}
	var scanStart uint64 = DataStart
	snapshotCorrupt := false
	snapshotLoaded := false
	if active.SnapshotOffset > 0 && active.SnapshotLength > 0 {
		if err := e.loadSnapshot(); err != nil {
			// Snapshot corrupt or out of bounds (reclaim checkpoint truncation window):
			// fall back to a full scan instead of refusing to open.
			e.index = make(map[uint64]uint64)
			e.recordCount = 0
			e.snapshotData = nil
			snapshotCorrupt = true
		} else {
			// Recover records appended after the snapshot (crash without checkpoint).
			snapshotLoaded = true
			scanStart = active.SnapshotOffset + uint64(active.SnapshotLength)
		}
	}
	end, truncate, err := e.scanRecords(scanStart)
	if err != nil {
		UnmapFile(e.mmap)
		f.Close()
		return nil, err
	}
	e.nextOffset = end
	if truncate {
		// Drop crash residue (torn frame or orphan snapshot blob) past the
		// last valid record so future appends start from a clean tail.
		if err := e.truncateTail(int64(end)); err != nil {
			UnmapFile(e.mmap)
			f.Close()
			return nil, err
		}
	}
	if snapshotLoaded && end == scanStart {
		// trimTailSnapshot must truncate at the record-area end (the start
		// of the first tail snapshot), not at the latest snapshot offset or
		// at the end of the snapshot chain. New headers store RecordEnd
		// directly; legacy files get a one-time reconstruction scan.
		e.nextOffset = e.recoverRecordAreaEnd()
	}
	if snapshotCorrupt {
		// Clear the active header snapshot pointer to avoid repeated out-of-bounds rescans on Open.
		hdr := copyHeader(e.activeHeaderRef())
		hdr.SnapshotOffset = 0
		hdr.SnapshotLength = 0
		hdr.CRC32 = hdr.calculateCRC()
		if err := writeHeaderAt(e.file, int64(e.activeHeader)*HeaderSize, hdr.ToBytes()); err != nil {
			UnmapFile(e.mmap)
			f.Close()
			return nil, err
		}
		if e.activeHeader == 0 {
			e.headerA = hdr
		} else {
			e.headerB = hdr
		}
	}
	e.recordCount = uint32(len(e.index))
	e.rebuildByType()
	return e, nil
}

func (e *StorageEngine) WriteRecord(recordType uint8, idHash uint64, data []byte) (uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return 0, common.NewError(common.ErrClosed, "engine is closed")
	}
	offsets, err := e.writeRecordBatch([]RecordEntry{{RecordType: recordType, IDHash: idHash, Data: data}})
	if err != nil {
		return 0, err
	}
	return offsets[0], nil
}

// WriteRecordBatch writes all records in one flush+remap cycle.
func (e *StorageEngine) WriteRecordBatch(records []RecordEntry) ([]uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, common.NewError(common.ErrClosed, "engine is closed")
	}
	return e.writeRecordBatch(records)
}

func (e *StorageEngine) ReadRecord(idHash uint64) (uint8, []byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return 0, nil, common.NewError(common.ErrClosed, "engine is closed")
	}
	offset, ok := e.index[idHash]
	if !ok {
		return 0, nil, common.NewError(common.ErrNotFound, "record not found")
	}
	rt, _, data, _, err := RecordData(e.mmap, offset)
	if err != nil {
		return 0, nil, err
	}
	return rt, data, nil
}

// DeleteRecord appends a FlagDeleted tombstone (same idHash, original type,
// empty data) and drops the record from the index.
func (e *StorageEngine) DeleteRecord(idHash uint64) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return false, common.NewError(common.ErrClosed, "engine is closed")
	}
	deleted, err := e.deleteRecordBatchLocked([]uint64{idHash})
	return deleted > 0, err
}

// DeleteRecordBatch deletes multiple records in one flush+remap cycle;
// already-missing ids are skipped. Returns the number deleted.
func (e *StorageEngine) DeleteRecordBatch(idHashes []uint64) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return 0, common.NewError(common.ErrClosed, "engine is closed")
	}
	return e.deleteRecordBatchLocked(idHashes)
}

// deleteRecordBatchLocked appends tombstones, syncs once, remaps once, then
// bulk-updates the index. Caller must hold e.mu.
func (e *StorageEngine) deleteRecordBatchLocked(idHashes []uint64) (int, error) {
	// Collect tombstones for existing records, keeping the original type for forensics.
	var tombstones []byte
	deleted := 0
	for _, idHash := range idHashes {
		offset, ok := e.index[idHash]
		if !ok {
			continue
		}
		var rt uint8
		if int(offset) < len(e.mmap) {
			rt = e.mmap[int(offset)]
		}
		tombstones = append(tombstones, EncodeRecord(rt, FlagDeleted, idHash, nil)...)
		deleted++
	}
	if deleted == 0 {
		return 0, nil
	}
	// Trim any trailing snapshot so record frames always precede snapshots.
	if err := e.trimTailSnapshot(); err != nil {
		return 0, err
	}
	end, err := e.file.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, common.NewError(common.ErrIO, "seek end", err)
	}
	if _, err := e.file.Write(tombstones); err != nil {
		return 0, common.NewError(common.ErrIO, "write tombstones", err)
	}
	if err := e.file.Sync(); err != nil {
		return 0, common.NewError(common.ErrIO, "sync tombstones", err)
	}
	mm, err := RemapFile(e.file, e.mmap)
	if err != nil {
		return 0, err
	}
	e.mmap = mm
	for _, idHash := range idHashes {
		offset, ok := e.index[idHash]
		if !ok {
			continue
		}
		delete(e.index, idHash)
		if int(offset) < len(e.mmap) {
			oldRT := e.mmap[int(offset)]
			if ids, ok := e.byType[oldRT]; ok {
				delete(ids, idHash)
				if len(ids) == 0 {
					delete(e.byType, oldRT)
				}
			}
		}
		e.recordCount--
	}
	e.nextOffset = uint64(end) + uint64(len(tombstones))
	e.dirty = true
	return deleted, nil
}

func (e *StorageEngine) Contains(idHash uint64) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return false
	}
	_, ok := e.index[idHash]
	return ok
}

// ScanDeletedPayloads returns, for every record of the given type whose
// newest frame is a tombstone, the payload of the newest non-tombstone
// frame (the pre-delete value). Frames already reclaimed or compacted are
// gone forever and cannot be recovered. Payloads are copied so callers may
// write them back without holding the mmap view.
func (e *StorageEngine) ScanDeletedPayloads(recordType uint8) (map[uint64][]byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return nil, common.NewError(common.ErrClosed, "engine is closed")
	}
	payload := make(map[uint64][]byte)
	deleted := make(map[uint64]bool)
	offset := uint64(DataStart)
	for offset < e.nextOffset {
		rt, flags, data, idHash, err := RecordData(e.mmap, offset)
		if err != nil {
			if errors.Is(err, io.EOF) || common.CodeOf(err) == common.ErrCRCMismatch {
				break // crash residue after the last clean frame
			}
			return nil, err
		}
		if rt == recordType {
			if flags&FlagDeleted != 0 {
				deleted[idHash] = true
			} else {
				deleted[idHash] = false
				payload[idHash] = append([]byte(nil), data...)
			}
		}
		offset += uint64(RecordHeaderSize) + uint64(len(data))
	}
	out := make(map[uint64][]byte)
	for id, del := range deleted {
		if del {
			if p, ok := payload[id]; ok {
				out[id] = p
			}
		}
	}
	return out, nil
}

// Checkpoint persists the index snapshot and switches A/B headers.
func (e *StorageEngine) Checkpoint(snap *IndexSnapshotData) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return common.NewError(common.ErrClosed, "engine is closed")
	}
	return e.checkpoint(snap)
}

// Close checkpoints, unmaps, and closes the file. All steps run even on
// failure; the first error wins (checkpoint > unmap > sync > close) so
// no mmap region or descriptor leaks.
func (e *StorageEngine) Close(snap *IndexSnapshotData) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return common.NewError(common.ErrClosed, "engine is closed")
	}
	e.closed = true
	ckptErr := e.checkpoint(snap)
	unmapErr := UnmapFile(e.mmap)
	e.mmap = nil
	syncErr := e.file.Sync()
	unlockErr := unlockFile(e.file)
	closeErr := e.file.Close()
	switch {
	case ckptErr != nil:
		return ckptErr
	case unmapErr != nil:
		return unmapErr
	case syncErr != nil:
		return common.NewError(common.ErrIO, "sync", syncErr)
	case unlockErr != nil:
		return unlockErr
	default:
		return closeErr
	}
}

// CloseNoCheckpoint unmaps and closes without a snapshot or A/B flip; the
// on-disk state stays as the last checkpoint plus appended records.
func (e *StorageEngine) CloseNoCheckpoint() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return common.NewError(common.ErrClosed, "engine is closed")
	}
	e.closed = true
	if err := UnmapFile(e.mmap); err != nil {
		return err
	}
	e.mmap = nil
	if err := unlockFile(e.file); err != nil {
		return err
	}
	return e.file.Close()
}

// IterIndex iterates over all (idHash, offset) pairs. The index is copied
// first and fn runs lock-free so it may call engine methods; iteration sees
// a snapshot. Return false from fn to stop.
func (e *StorageEngine) IterIndex(fn func(idHash, offset uint64) bool) {
	e.mu.RLock()
	if e.closed {
		e.mu.RUnlock()
		return
	}
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

// IterIndexByType iterates all idHashes of a record type over a snapshot;
// fn runs lock-free. Returns the first error from fn.
func (e *StorageEngine) IterIndexByType(rt uint8, fn func(idHash uint64) error) error {
	e.mu.RLock()
	if e.closed {
		e.mu.RUnlock()
		return common.NewError(common.ErrClosed, "engine is closed")
	}
	ids := make([]uint64, 0, len(e.byType[rt]))
	for id := range e.byType[rt] {
		ids = append(ids, id)
	}
	e.mu.RUnlock()
	for _, id := range ids {
		if err := fn(id); err != nil {
			return err
		}
	}
	return nil
}

func (e *StorageEngine) RecordCount() uint32 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.recordCount
}

func (e *StorageEngine) VectorDim() uint16 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeHeaderRef().VectorDim
}

func (e *StorageEngine) FileSize() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return uint64(len(e.mmap))
}

func (e *StorageEngine) SnapshotData() *IndexSnapshotData {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snapshotData
}

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
	// Trim any trailing snapshot so record frames always precede snapshots.
	if err := e.trimTailSnapshot(); err != nil {
		return nil, err
	}
	// Append first; index, recordCount and mmap update only after writes,
	// sync and remap succeed, so a mid-batch failure leaves consistent state.
	offsets := make([]uint64, 0, len(records))
	for _, rec := range records {
		encoded := EncodeRecord(rec.RecordType, 0, rec.IDHash, rec.Data)
		offset, err := e.file.Seek(0, io.SeekEnd)
		if err != nil {
			return nil, common.NewError(common.ErrIO, "seek end", err)
		}
		if _, err := e.file.Write(encoded); err != nil {
			return nil, common.NewError(common.ErrIO, "write record", err)
		}
		offsets = append(offsets, uint64(offset))
	}
	if err := e.file.Sync(); err != nil {
		return nil, common.NewError(common.ErrIO, "sync", err)
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
		if oldOff, exists := e.index[rec.IDHash]; exists {
			if int(oldOff) < len(e.mmap) {
				oldRT := e.mmap[int(oldOff)]
				if oldRT != rec.RecordType {
					delete(e.byType[oldRT], rec.IDHash)
				}
			}
		}
		e.index[rec.IDHash] = offsets[i]
		if e.byType[rec.RecordType] == nil {
			e.byType[rec.RecordType] = make(map[uint64]struct{})
		}
		e.byType[rec.RecordType][rec.IDHash] = struct{}{}
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
		return common.NewError(common.ErrIO, "seek snap", err)
	}
	if _, err := e.file.Write(blob); err != nil {
		return common.NewError(common.ErrIO, "write snapshot", err)
	}
	if err := e.file.Sync(); err != nil {
		return common.NewError(common.ErrIO, "sync", err)
	}
	mm, err := RemapFile(e.file, e.mmap)
	if err != nil {
		return err
	}
	e.mmap = mm
	newHdr := e.buildCheckpointHeader(snapOffset, uint32(len(blob)))
	if err := e.writeInactiveHeader(newHdr); err != nil {
		return err
	}
	mm, err = RemapFile(e.file, e.mmap)
	if err != nil {
		return err
	}
	e.mmap = mm
	e.switchHeader(newHdr)
	return nil
}

func (e *StorageEngine) buildCheckpointHeader(snapOffset int64, snapLen uint32) *FileHeader {
	h := copyHeader(e.activeHeaderRef())
	h.CommitID++
	h.SnapshotOffset = uint64(snapOffset)
	h.SnapshotLength = snapLen
	h.RecordCount = e.recordCount
	h.RecordEnd = e.nextOffset
	h.CRC32 = h.calculateCRC()
	return h
}

func (e *StorageEngine) loadSnapshot() error {
	active := e.activeHeaderRef()
	off := int(active.SnapshotOffset)
	length := int(active.SnapshotLength)
	if off < DataStart || off+length > len(e.mmap) {
		return common.NewError(common.ErrCorruption, "snapshot out of bounds")
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
	return nil
}

// scanRecords scans from offset, merging records into the index (a later
// same-idHash overrides; a tombstone deletes). Stops at the first truncated
// or CRC-failed frame (crash residue) and reports whether it must be truncated.
func (e *StorageEngine) scanRecords(start uint64) (end uint64, truncate bool, err error) {
	offset := start
	for {
		_, flags, data, idHash, err := RecordData(e.mmap, offset)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if c := common.CodeOf(err); c == common.ErrCRCMismatch || c == common.ErrCorruption {
				return offset, true, nil
			}
			return 0, false, err
		}
		if flags&FlagDeleted != 0 {
			delete(e.index, idHash)
		} else {
			e.index[idHash] = offset
		}
		offset += uint64(RecordHeaderSize) + uint64(len(data))
	}
	return offset, false, nil
}

// recoverRecordAreaEnd returns the end of the record area for files with a
// valid tail snapshot. New headers carry RecordEnd directly; legacy files
// (RecordEnd == 0) are reconstructed by walking record frames and skipping
// snapshot blobs until the end of the file. This matters when several
// snapshots are chained at the tail: trimming at the latest snapshot offset
// would leave older snapshots behind and recreate the append/crash data-loss
// window.
func (e *StorageEngine) recoverRecordAreaEnd() uint64 {
	active := e.activeHeaderRef()
	if active.RecordEnd >= DataStart &&
		active.RecordEnd <= active.SnapshotOffset &&
		active.RecordEnd <= uint64(len(e.mmap)) {
		return active.RecordEnd
	}

	offset := uint64(DataStart)
	recordEnd := uint64(DataStart)
	for offset < uint64(len(e.mmap)) {
		if isSnapshotBlobAt(e.mmap[offset:]) {
			n, err := snapshotBlobLength(e.mmap[offset:])
			if err != nil {
				return active.SnapshotOffset
			}
			offset += uint64(n)
			continue
		}
		_, _, data, _, err := RecordData(e.mmap, offset)
		if err != nil {
			return active.SnapshotOffset
		}
		offset += uint64(RecordHeaderSize) + uint64(len(data))
		recordEnd = offset
	}
	if offset != uint64(len(e.mmap)) {
		// Zero padding or an unrecognized tail: do not guess a truncation
		// point that could drop existing records.
		return active.SnapshotOffset
	}
	return recordEnd
}

// truncateTail shrinks the file and remaps, discarding crash residue.
// Caller must hold e.mu.
func (e *StorageEngine) truncateTail(size int64) error {
	if err := UnmapFile(e.mmap); err != nil {
		return err
	}
	e.mmap = nil
	if err := e.file.Truncate(size); err != nil {
		return common.NewError(common.ErrIO, "truncate crash residue", err)
	}
	if err := e.file.Sync(); err != nil {
		return common.NewError(common.ErrIO, "sync after truncate", err)
	}
	mm, err := MapFile(e.file, int(size))
	if err != nil {
		return err
	}
	e.mmap = mm
	return nil
}

// rebuildByType rebuilds the byType secondary index. Caller must hold e.mu.
func (e *StorageEngine) rebuildByType() {
	e.byType = make(map[uint8]map[uint64]struct{})
	for id, off := range e.index {
		if int(off) >= len(e.mmap) {
			continue
		}
		rt := e.mmap[int(off)]
		if e.byType[rt] == nil {
			e.byType[rt] = make(map[uint64]struct{})
		}
		e.byType[rt][id] = struct{}{}
	}
}

// writeInactiveHeader writes hdr to the inactive A/B slot and syncs it.
// Caller must hold e.mu.
func (e *StorageEngine) writeInactiveHeader(hdr *FileHeader) error {
	writeOffset := int64(HeaderAOffset)
	if e.activeHeader != 1 {
		writeOffset = HeaderBOffset
	}
	if err := writeHeaderAt(e.file, writeOffset, hdr.ToBytes()); err != nil {
		return err
	}
	if err := e.file.Sync(); err != nil {
		return common.NewError(common.ErrIO, "sync header", err)
	}
	return nil
}

// switchHeader makes hdr the active header. Caller must hold e.mu.
func (e *StorageEngine) switchHeader(hdr *FileHeader) {
	if e.activeHeader == 1 {
		e.headerA = hdr
		e.activeHeader = 0
	} else {
		e.headerB = hdr
		e.activeHeader = 1
	}
}
