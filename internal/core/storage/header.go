// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package storage

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"

	"github.com/qyiun666/MemHop/internal/common/mherrors"
)

// A/B dual header layout constants.
const (
	HeaderSize    = 4096
	HeaderAOffset = 0
	HeaderBOffset = 4096
	DataStart     = 8192
)

// FormatVersion is the on-disk file format version.
const FormatVersion uint16 = 0x0002

// Magic bytes for file identification.
var (
	Magic     = [4]byte{'M', 'E', 'H', '2'}
	TailMagic = [4]byte{'2', 'H', 'E', 'M'}
)

// FileHeader is the on-disk file header (4096 bytes).
//
// Layout:
//
//	[0..4)     magic
//	[4..6)     version (le16)
//	[6..8)     vector_dim (le16)
//	[8..16)    commit_id (le64)
//	[16..24)   snapshot_offset (le64)
//	[24..28)   snapshot_length (le32)
//	[28..32)   record_count (le32)
//	[32..36)   flags (le32)
//	[36..4088) reserved (zeroed)
//	[4088..4092) crc32 (le32)
//	[4092..4096) tail_magic
type FileHeader struct {
	Version        uint16
	VectorDim      uint16
	CommitID       uint64
	SnapshotOffset uint64
	SnapshotLength uint32
	RecordCount    uint32
	Flags          uint32
	CRC32          uint32
}

// NewFileHeader creates a fresh header with the given vector dimension.
func NewFileHeader(vectorDim uint16) *FileHeader {
	return &FileHeader{Version: FormatVersion, VectorDim: vectorDim}
}

// ToBytes serializes the header into a 4096-byte buffer with CRC32.
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
	// reserved [36..4088) stays zero
	crc := crc32.ChecksumIEEE(buf[:4088])
	binary.LittleEndian.PutUint32(buf[4088:4092], crc)
	copy(buf[4092:4096], TailMagic[:])
	return buf
}

// FileHeaderFromBytes deserializes a header from a 4096-byte buffer.
func FileHeaderFromBytes(buf [HeaderSize]byte) (*FileHeader, error) {
	if buf[0] != Magic[0] || buf[1] != Magic[1] || buf[2] != Magic[2] || buf[3] != Magic[3] {
		return nil, mherrors.ErrInvalidMagic
	}
	if buf[4092] != TailMagic[0] || buf[4093] != TailMagic[1] ||
		buf[4094] != TailMagic[2] || buf[4095] != TailMagic[3] {
		return nil, mherrors.ErrInvalidMagic
	}
	storedCRC := binary.LittleEndian.Uint32(buf[4088:4092])
	if crc32.ChecksumIEEE(buf[:4088]) != storedCRC {
		return nil, mherrors.ErrCRCMismatch
	}
	version := binary.LittleEndian.Uint16(buf[4:6])
	if version != FormatVersion {
		return nil, mherrors.NewError(mherrors.ErrCorruption,
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
		CRC32:          storedCRC,
	}, nil
}

// calculateCRC returns what the CRC32 should be for this header.
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
		return nil, mherrors.ErrCRCMismatch
	}
}
