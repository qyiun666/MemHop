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

// FormatVersion is the on-disk file format version. Files below 0x0012 are
// rejected at Open with no migration path: a 0x0011 profile carries no
// agent_type, so every domain in such a file decodes as the primary agent —
// not one wrong value somewhere but the same wrong value in every domain at
// once, against the rule that a file holds exactly one primary, and nothing
// reading the file can tell that apart from a file that genuinely means it.
// That is why a boundary version cannot be tolerated the way a missing
// optional field can. The per-version change history lives in this constant's
// git log, not in this comment.
const FormatVersion uint16 = 0x0012

var (
	Magic     = [4]byte{'M', 'E', 'H', '2'}
	TailMagic = [4]byte{'2', 'H', 'E', 'M'}
)

// FileHeader is the on-disk file header (4096 bytes).
// Layout: magic(4) version(2) reserved(2) commit_id(8) snapshot_off(8)
// snapshot_len(4) record_count(4) flags(4) record_end(8) reserved
// crc32(4) tail_magic(4).
// The bytes at offset 6 are reserved: new files write 0, a legacy file's value
// is kept as-is, and nothing reads them — neither the version check nor the
// A/B header choice (CRC + CommitID decide).
// RecordEnd is the end of the record area (start of the first tail snapshot);
// zero means "unknown" and Open reconstructs it with a one-time scan, a
// defence against torn or hand-edited headers.
type FileHeader struct {
	Version        uint16
	Reserved       uint16
	CommitID       uint64
	SnapshotOffset uint64
	SnapshotLength uint32
	RecordCount    uint32
	Flags          uint32
	RecordEnd      uint64
	CRC32          uint32
}

func NewFileHeader() *FileHeader {
	return &FileHeader{Version: FormatVersion}
}

func (h *FileHeader) ToBytes() [HeaderSize]byte {
	var buf [HeaderSize]byte
	copy(buf[0:4], Magic[:])
	binary.LittleEndian.PutUint16(buf[4:6], h.Version)
	binary.LittleEndian.PutUint16(buf[6:8], h.Reserved)
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
		Reserved:       binary.LittleEndian.Uint16(buf[6:8]),
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

// SelectValidHeader picks the header with the highest commit id. Both arguments
// come from FileHeaderFromBytes, which is the one place a CRC mismatch is
// refused — a header that reached this far cannot also be an invalid one, so
// re-deriving its checksum here only cost two serializations per Open.
func SelectValidHeader(a, b *FileHeader) *FileHeader {
	if a.CommitID >= b.CommitID {
		return a
	}
	return b
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
		active := SelectValidHeader(a, b)
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
