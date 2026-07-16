// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package storage

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/qyiun666/memhop/memhop/internal/core"
)

// RecordHeaderSize is type(1) + flags(1) + length(4) + id_hash(8) = 14 bytes.
const RecordHeaderSize = 14

// FlagDeleted marks a record as logically deleted.
const FlagDeleted uint8 = 0x01

// Record type constants.
const (
	RecL0Profile     uint8 = 0x01
	RecL1SceneNode   uint8 = 0x02
	RecL1Hyperedge   uint8 = 0x03
	RecL2Topic       uint8 = 0x04
	RecL2Scene       uint8 = 0x05
	RecL3GraphNode   uint8 = 0x06
	RecL3GraphEdge   uint8 = 0x07
	RecL4Archive     uint8 = 0x08
	RecL5ActionChain uint8 = 0x09
	RecL3GraphSlot   uint8 = 0x0B
	RecL5ActionStep  uint8 = 0x0C
	RecVecCentroid   uint8 = 0xF0 // centroid vector for brute-force search
)

// EncodeRecord encodes a record into a byte slice.
func EncodeRecord(recordType, flags uint8, idHash uint64, data []byte) []byte {
	buf := make([]byte, RecordHeaderSize+len(data))
	buf[0] = recordType
	buf[1] = flags
	binary.LittleEndian.PutUint32(buf[2:6], uint32(len(data)))
	binary.LittleEndian.PutUint64(buf[6:14], idHash)
	copy(buf[RecordHeaderSize:], data)
	return buf
}

// RecordData decodes a record from a byte slice at the given offset.
// Returns a **copy** of the data to be GC-safe (not a sub-slice of mmap).
// An offset exactly at the end of the mapped region, or an all-zero header
// (never-written space), reports io.EOF; a truncated record header or body
// in the middle of the file reports corruption.
func RecordData(mmap []byte, offset uint64) (recordType, flags uint8, data []byte, idHash uint64, err error) {
	off := int(offset)
	if off == len(mmap) {
		return 0, 0, nil, 0, io.EOF
	}
	if off+RecordHeaderSize > len(mmap) {
		return 0, 0, nil, 0, core.NewError(
			core.ErrCorruption,
			fmt.Sprintf("record header at offset %d exceeds file size %d", offset, len(mmap)),
		)
	}
	recordType = mmap[off]
	flags = mmap[off+1]
	dataLen := int(binary.LittleEndian.Uint32(mmap[off+2 : off+6]))
	idHash = binary.LittleEndian.Uint64(mmap[off+6 : off+14])
	if recordType == 0 && flags == 0 && dataLen == 0 && idHash == 0 {
		return 0, 0, nil, 0, io.EOF // zero-filled space counts as end of data
	}
	dataEnd := off + RecordHeaderSize + dataLen
	if dataEnd > len(mmap) {
		return 0, 0, nil, 0, core.NewError(
			core.ErrCorruption,
			fmt.Sprintf("record at offset %d claims length %d but file ends at %d", offset, dataLen, len(mmap)),
		)
	}
	// Return a copy for GC safety.
	data = make([]byte, dataLen)
	copy(data, mmap[off+RecordHeaderSize:dataEnd])
	return
}
