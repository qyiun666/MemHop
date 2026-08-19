// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"

	"github.com/qyiun666/MemHop/internal/common"
)

const (
	HeaderSize    = 4096
	HeaderAOffset = 0
	HeaderBOffset = 4096
	DataStart     = 8192
)

// FormatVersion is the on-disk file format version. 0x0005 introduced the
// L5 capability record (0x0F) whose payload schema replaced the v1.2.0
// PluginSlot; 0x0006 re-designed the capability payload as the v2
// mcp/skill/composite resource-wrapper model. Files with 0x0005 (or older)
// are rejected at Open — there is no migration path for the old payloads.
const FormatVersion uint16 = 0x0006

var (
	Magic     = [4]byte{'M', 'E', 'H', '2'}
	TailMagic = [4]byte{'2', 'H', 'E', 'M'}
)

// FileHeader is the on-disk file header (4096 bytes).
// Layout: magic(4) version(2) vector_dim(2) commit_id(8) snapshot_off(8)
// snapshot_len(4) record_count(4) flags(4) record_end(8) reserved
// crc32(4) tail_magic(4).
// RecordEnd is the end of the record area (start of the first tail
// snapshot). It is always written by 0x0005+ checkpoints; zero means
// "unknown" and Open reconstructs it with a one-time scan as a defensive
// measure against torn or hand-edited headers.
type FileHeader struct {
	Version        uint16
	VectorDim      uint16
	CommitID       uint64
	SnapshotOffset uint64
	SnapshotLength uint32
	RecordCount    uint32
	Flags          uint32
	RecordEnd      uint64
	CRC32          uint32
}

func NewFileHeader(vectorDim uint16) *FileHeader {
	return &FileHeader{Version: FormatVersion, VectorDim: vectorDim}
}

func (h *FileHeader) ToBytes() [HeaderSize]byte {
	var buf [HeaderSize]byte
	copy(buf[0:4], Magic[:])
	binary.LittleEndian.PutUint16(buf[4:6], h.Version)
	binary.LittleEndian.PutUint16(buf[6:8], h.VectorDim)
	binary.LittleEndian.PutUint64(buf[8:16], h.CommitID)
	binary.LittleEndian.PutUint64(buf[16:24], h.SnapshotOffset)
	binary.LittleEndian.PutUint32(buf[24:28], h.SnapshotLength)
	binary.LittleEndian.PutUint32(buf[28:32], h.RecordCount)
	binary.LittleEndian.PutUint32(buf[32:36], h.Flags)
	binary.LittleEndian.PutUint64(buf[36:44], h.RecordEnd)
	crc := crc32.ChecksumIEEE(buf[:4088])
	binary.LittleEndian.PutUint32(buf[4088:4092], crc)
	copy(buf[4092:4096], TailMagic[:])
	return buf
}

func FileHeaderFromBytes(buf [HeaderSize]byte) (*FileHeader, error) {
	if buf[0] != Magic[0] || buf[1] != Magic[1] || buf[2] != Magic[2] || buf[3] != Magic[3] {
		return nil, common.NewError(common.ErrInvalidMagic, "invalid magic bytes")
	}
	if buf[4092] != TailMagic[0] || buf[4093] != TailMagic[1] ||
		buf[4094] != TailMagic[2] || buf[4095] != TailMagic[3] {
		return nil, common.NewError(common.ErrInvalidMagic, "invalid magic bytes")
	}
	storedCRC := binary.LittleEndian.Uint32(buf[4088:4092])
	if crc32.ChecksumIEEE(buf[:4088]) != storedCRC {
		return nil, common.NewError(common.ErrCRCMismatch, "crc32 mismatch")
	}
	version := binary.LittleEndian.Uint16(buf[4:6])
	if version != FormatVersion {
		return nil, common.NewError(common.ErrCorruption,
			fmt.Sprintf("unsupported file format version 0x%04x (expected 0x%04x)", version, FormatVersion))
	}
	return &FileHeader{
		Version:        version,
		VectorDim:      binary.LittleEndian.Uint16(buf[6:8]),
		CommitID:       binary.LittleEndian.Uint64(buf[8:16]),
		SnapshotOffset: binary.LittleEndian.Uint64(buf[16:24]),
		SnapshotLength: binary.LittleEndian.Uint32(buf[24:28]),
		RecordCount:    binary.LittleEndian.Uint32(buf[28:32]),
		Flags:          binary.LittleEndian.Uint32(buf[32:36]),
		RecordEnd:      binary.LittleEndian.Uint64(buf[36:44]),
		CRC32:          storedCRC,
	}, nil
}

func (h *FileHeader) calculateCRC() uint32 {
	b := h.ToBytes()
	return crc32.ChecksumIEEE(b[:4088])
}

// SelectValidHeader picks the header with the highest commitID among valid ones.
func SelectValidHeader(a, b *FileHeader) (*FileHeader, error) {
	aValid := a.CRC32 == a.calculateCRC()
	bValid := b.CRC32 == b.calculateCRC()
	switch {
	case aValid && bValid:
		if a.CommitID >= b.CommitID {
			return a, nil
		}
		return b, nil
	case aValid:
		return a, nil
	case bValid:
		return b, nil
	default:
		return nil, common.NewError(common.ErrCRCMismatch, "crc32 mismatch")
	}
}

func writeHeaderAt(f *os.File, offset int64, buf [HeaderSize]byte) error {
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return common.NewError(common.ErrIO, "seek header", err)
	}
	if _, err := f.Write(buf[:]); err != nil {
		return common.NewError(common.ErrIO, "write header", err)
	}
	return nil
}

func loadHeaders(mm []byte) (hA, hB *FileHeader, activeIdx uint8, err error) {
	var bufA, bufB [HeaderSize]byte
	copy(bufA[:], mm[:HeaderSize])
	copy(bufB[:], mm[HeaderSize:HeaderSize*2])

	a, errA := FileHeaderFromBytes(bufA)
	b, errB := FileHeaderFromBytes(bufB)
	switch {
	case errA == nil && errB == nil:
		active, err := SelectValidHeader(a, b)
		if err != nil {
			return nil, nil, 0, err
		}
		if active.CommitID == a.CommitID {
			return a, b, 0, nil
		}
		return a, b, 1, nil
	case errA == nil:
		// Header B is torn/corrupt, but A is valid: recover with A. The
		// in-memory B slot starts as a copy of A so it is never nil.
		return a, copyHeader(a), 0, nil
	case errB == nil:
		// Header A is torn/corrupt, but B is valid: recover with B.
		return copyHeader(b), b, 1, nil
	default:
		return nil, nil, 0, common.NewError(common.ErrCorruption,
			fmt.Sprintf("both file headers are invalid (A: %v; B: %v)", errA, errB))
	}
}

func copyHeader(h *FileHeader) *FileHeader {
	c := *h
	return &c
}
