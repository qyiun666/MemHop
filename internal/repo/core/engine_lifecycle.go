// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Engine lifecycle: create/open (with crash-consistent index restore) and
// checkpoint/close. Index restore helpers live next to their callers;
// frame scanning and tail recovery are in engine_recovery.go.

package core

import (
	"io"
	"log/slog"
	"os"

	"github.com/qyiun666/MemHop/internal/common"
)

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
		index:        make(map[uint64]map[uint64]uint64),
		byAgentType:  make(map[uint64]map[uint8]map[uint64]struct{}),
		nextOffset:   DataStart,
	}, nil
}

func Open(path string) (*StorageEngine, error) {
	f, mm, hA, hB, activeIdx, err := openEngineFile(path)
	if err != nil {
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
		index:        make(map[uint64]map[uint64]uint64),
		byAgentType:  make(map[uint64]map[uint8]map[uint64]struct{}),
		recordCount:  active.RecordCount,
		nextOffset:   DataStart,
	}
	scanStart, snapshotLoaded, snapshotCorrupt := e.restoreFromSnapshot(active)
	end, truncate, err := e.scanRecords(scanStart)
	if err == nil {
		e.nextOffset = end
		if truncate {
			// Drop crash residue (torn frame or orphan snapshot blob) past the
			// last valid record so future appends start from a clean tail.
			err = e.truncateTail(int64(end))
		} else if snapshotLoaded && end == scanStart {
			// trimTailSnapshot must truncate at the record-area end (the start
			// of the first tail snapshot), not at the latest snapshot offset or
			// at the end of the snapshot chain. New headers store RecordEnd
			// directly; legacy files get a one-time reconstruction scan.
			e.nextOffset = e.recoverRecordAreaEnd()
		}
	}
	if err == nil && snapshotCorrupt {
		// Clear the active header snapshot pointer to avoid repeated
		// out-of-bounds rescans on Open.
		err = e.clearActiveSnapshot()
	}
	if err != nil {
		e.abortOpen()
		return nil, err
	}
	e.recordCount = uint32(e.totalRecordsLocked())
	e.rebuildByAgentType()
	return e, nil
}

// openEngineFile opens and exclusively locks the file, maps it and loads
// the A/B headers; every failure path releases what it already acquired.
func openEngineFile(path string) (*os.File, []byte, *FileHeader, *FileHeader, uint8, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return nil, nil, nil, nil, 0, common.NewError(common.ErrIO, "open file", err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, nil, nil, nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, nil, nil, 0, common.NewError(common.ErrIO, "stat", err)
	}
	if info.Size() < HeaderSize*2 {
		f.Close()
		return nil, nil, nil, nil, 0, common.NewError(common.ErrCorruption, "file too small for dual headers")
	}
	mm, err := MapFile(f, int(info.Size()))
	if err != nil {
		f.Close()
		return nil, nil, nil, nil, 0, err
	}
	hA, hB, activeIdx, err := loadHeaders(mm)
	if err != nil {
		UnmapFile(mm)
		f.Close()
		return nil, nil, nil, nil, 0, err
	}
	return f, mm, hA, hB, activeIdx, nil
}

// restoreFromSnapshot loads the snapshot referenced by the active header
// and reports where the incremental record scan must start plus whether a
// snapshot was consumed or found corrupt.
func (e *StorageEngine) restoreFromSnapshot(active *FileHeader) (scanStart uint64, loaded, corrupt bool) {
	if active.SnapshotOffset == 0 || active.SnapshotLength == 0 {
		return DataStart, false, false
	}
	if err := e.loadSnapshot(); err != nil {
		// Snapshot corrupt or out of bounds (reclaim checkpoint truncation
		// window): fall back to a full scan instead of refusing to open.
		// Loud, so corruption never stays invisible behind a healthy Open.
		slog.Warn("engine: snapshot unreadable, rebuilding index by full scan",
			"err", err)
		e.index = make(map[uint64]map[uint64]uint64)
		e.recordCount = 0
		e.snapshotData = nil
		return DataStart, false, true
	}
	// Recover records appended after the snapshot (crash without checkpoint).
	return active.SnapshotOffset + uint64(active.SnapshotLength), true, false
}

// clearActiveSnapshot zeroes the active header's snapshot pointers so a
// corrupt snapshot is not rescanned out of bounds on every Open.
func (e *StorageEngine) clearActiveSnapshot() error {
	hdr := copyHeader(e.activeHeaderRef())
	hdr.SnapshotOffset = 0
	hdr.SnapshotLength = 0
	hdr.CRC32 = hdr.calculateCRC()
	if err := writeHeaderAt(e.file, int64(e.activeHeader)*HeaderSize, hdr.ToBytes()); err != nil {
		return err
	}
	if e.activeHeader == 0 {
		e.headerA = hdr
	} else {
		e.headerB = hdr
	}
	return nil
}

// abortOpen releases all OS resources after a failed Open; the engine is
// never published to callers in that path.
func (e *StorageEngine) abortOpen() {
	UnmapFile(e.mmap)
	e.file.Close()
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
	shutErr := e.shutdownHandles()
	if ckptErr != nil {
		return ckptErr
	}
	return shutErr
}

// shutdownHandles unmaps, syncs, unlocks and closes the file, running every
// step even on failure; the first error wins so nothing leaks.
func (e *StorageEngine) shutdownHandles() error {
	unmapErr := UnmapFile(e.mmap)
	e.mmap = nil
	syncErr := e.file.Sync()
	unlockErr := unlockFile(e.file)
	closeErr := e.file.Close()
	switch {
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

// checkpoint appends the snapshot blob and flips the A/B header. Caller
// must hold e.mu.
func (e *StorageEngine) checkpoint(snap *IndexSnapshotData) error {
	blob, err := BuildSnapshot(e.index, snap)
	if err != nil {
		return err
	}
	return e.appendSnapshot(blob)
}

// appendSnapshot writes blob as the single tail snapshot behind the record
// area, syncs, remaps and flips to a header pointing at it. Caller must
// hold e.mu.
func (e *StorageEngine) appendSnapshot(blob []byte) error {
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
	e.recordCount = uint32(e.totalRecordsLocked())
	e.snapshotData = snap
	return nil
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
