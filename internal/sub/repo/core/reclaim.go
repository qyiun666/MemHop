// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"encoding/binary"
	"io"

	"github.com/qyiun666/MemHop/internal/sub/common"
)

// ReclaimMinSnapshots 快照数量低于该值时 CheckpointReclaim 直接返回不回收。
const ReclaimMinSnapshots = 10

// CheckpointReclaim 回收式检查点：删除文件中的全部旧快照，仅保留本次
// 最新快照（truncate 到数据区终点后重写），文件回落为 [数据帧区][单份快照]。
// 供 Dream 等低频周期调用以控制文件膨胀。
//
// 前置条件：文件尾必须为快照区（写路径的 trimTailSnapshot 维持的不变式）。
// 旧布局文件（快照与记录交错）返回 ErrInvalidQuery，需走 Compact 回收。
//
// 崩溃安全：truncate 后先写入无快照头（CommitID++，快照指针清零）再追加
// 新快照，任何时刻崩溃都由 Open 兜底恢复——无快照头触发全文件扫描重建，
// 越界快照头触发回退全扫（见 Open 的 loadSnapshot 容错分支）。
//
// 返回本次写入的快照数据，调用方可直接覆盖其内存快照副本。
func (e *StorageEngine) CheckpointReclaim(snap *IndexSnapshotData) (*IndexSnapshotData, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, common.NewError(common.ErrClosed, "engine is closed")
	}
	h := e.activeHeaderRef()
	if h.SnapshotOffset > 0 &&
		uint64(h.SnapshotOffset)+uint64(h.SnapshotLength) != uint64(len(e.mmap)) {
		return nil, common.NewError(common.ErrInvalidQuery,
			"snapshot not at file tail; run Compact instead")
	}
	n, err := e.countTailSnapshots()
	if err != nil {
		return nil, err
	}
	if n < ReclaimMinSnapshots {
		return snap, nil // 快照不足阈值，无需回收
	}
	blob, err := BuildSnapshot(e.index, snap)
	if err != nil {
		return nil, err
	}
	// 1. 截断到数据区终点（= nextOffset，写路径维持"记录帧恒在快照之前"的
	// 布局不变式）：删除文件尾的全部旧快照，保留所有有效记录帧。
	if err := e.truncateTail(int64(e.nextOffset)); err != nil {
		return nil, err
	}
	// 2. 清空快照指针：截断窗口崩溃时 Open 走全文件扫描而非越界读已删快照。
	if err := e.writeNullSnapshotHeader(); err != nil {
		return nil, err
	}
	// 3. 追加本次新快照。
	snapOffset, err := e.file.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, common.NewError(common.ErrIO, "seek snap", err)
	}
	if _, err := e.file.Write(blob); err != nil {
		return nil, common.NewError(common.ErrIO, "write snapshot", err)
	}
	if err := e.file.Sync(); err != nil {
		return nil, common.NewError(common.ErrIO, "sync", err)
	}
	mm, err := RemapFile(e.file, e.mmap)
	if err != nil {
		return nil, err
	}
	e.mmap = mm
	// 4. 写入带快照头（inactive 槽）并切换 active。
	newHdr := e.buildCheckpointHeader(snapOffset, uint32(len(blob)))
	if err := e.writeInactiveHeader(newHdr); err != nil {
		return nil, err
	}
	mm, err = RemapFile(e.file, e.mmap)
	if err != nil {
		return nil, err
	}
	e.mmap = mm
	e.switchHeader(newHdr)
	e.snapshotData = snap
	return snap, nil
}

// countTailSnapshots 统计帧区终点（nextOffset）之后连续排列的快照数量。
// 遇到非快照数据（残帧等）即停止。调用方必须持有 e.mu。
func (e *StorageEngine) countTailSnapshots() (int, error) {
	count := 0
	pos := e.nextOffset
	for pos < uint64(len(e.mmap)) {
		n, err := snapshotBlobLength(e.mmap[pos:])
		if err != nil {
			return count, nil
		}
		pos += uint64(n)
		count++
	}
	return count, nil
}

// snapshotBlobLength 解析快照 blob 的总长度（magic+version+count+entries+blobs+crc）。
func snapshotBlobLength(raw []byte) (int, error) {
	if len(raw) < 13 {
		return 0, common.NewError(common.ErrCorruption, "snapshot too short")
	}
	if binary.LittleEndian.Uint32(raw[0:4]) != SnapshotMagic || raw[4] != SnapshotVersion {
		return 0, common.NewError(common.ErrCorruption, "not a snapshot blob")
	}
	count := int(binary.LittleEndian.Uint32(raw[5:9]))
	pos := 9 + count*16
	for i := 0; i < 3; i++ {
		if pos+4 > len(raw) {
			return 0, common.NewError(common.ErrCorruption, "snapshot blob truncated")
		}
		blen := int(binary.LittleEndian.Uint32(raw[pos : pos+4]))
		pos += 4 + blen
	}
	if pos+4 > len(raw) {
		return 0, common.NewError(common.ErrCorruption, "snapshot crc truncated")
	}
	return pos + 4, nil
}

// trimTailSnapshot 若文件尾为快照区则截断删除并清空快照指针，维持
// "记录帧恒在快照之前"的布局不变式。checkpoint 后首次写记录/删除时调用。
// 调用方必须持有 e.mu。
func (e *StorageEngine) trimTailSnapshot() error {
	h := e.activeHeaderRef()
	if h.SnapshotOffset == 0 || h.SnapshotLength == 0 {
		return nil
	}
	if uint64(h.SnapshotOffset)+uint64(h.SnapshotLength) != uint64(len(e.mmap)) {
		return nil // 快照不在文件尾（旧布局），交由 Reclaim/Compact 处理
	}
	// nextOffset 在 checkpoint 后保持不变，恒等于帧区终点（第一份快照起点），
	// 一次截断可删除全部连续快照。
	if err := e.truncateTail(int64(e.nextOffset)); err != nil {
		return err
	}
	return e.writeNullSnapshotHeader()
}

// writeNullSnapshotHeader 把无快照头（CommitID++，快照指针清零）写入 inactive
// 槽并切换 active。调用方必须持有 e.mu。
func (e *StorageEngine) writeNullSnapshotHeader() error {
	nullHdr := copyHeader(e.activeHeaderRef())
	nullHdr.CommitID++
	nullHdr.SnapshotOffset = 0
	nullHdr.SnapshotLength = 0
	nullHdr.CRC32 = nullHdr.calculateCRC()
	if err := e.writeInactiveHeader(nullHdr); err != nil {
		return err
	}
	e.switchHeader(nullHdr)
	return nil
}

// Compact creates a new file at newPath containing only live records.
// snap must carry the caller's current serialized indices: compacting with
// an empty snapshot would silently drop the sparse/L1/L3 index data.
func (e *StorageEngine) Compact(newPath string, snap *IndexSnapshotData) error {
	if snap == nil {
		return common.NewError(common.ErrInvalidQuery, "compact requires an index snapshot")
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
			return common.NewError(common.ErrCorruption, "compact: read live record", readErr)
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
