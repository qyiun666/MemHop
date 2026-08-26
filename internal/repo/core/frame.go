// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"

	"github.com/qyiun666/MemHop/internal/common"
)

// RecordHeaderSize is type(1)+flags(1)+length(4)+agent_id(8)+id_hash(8)+crc32(4)
// = 26 bytes; CRC covers header plus data so a torn write is detected on
// Open. 0x0008 added agent_id to the frame so one file hosts multiple
// agents with physically separated record domains.
const RecordHeaderSize = 26

const FlagDeleted uint8 = 0x01

// Record type constants.
const (
	RecL0Profile    uint8 = 0x01
	RecL1SceneNode  uint8 = 0x02
	RecL1Hyperedge  uint8 = 0x03
	RecL2Topic      uint8 = 0x04
	RecL2Scene      uint8 = 0x05
	RecL3GraphNode  uint8 = 0x06
	RecL3GraphEdge  uint8 = 0x07
	RecL4Archive    uint8 = 0x08
	RecL3GraphSlot  uint8 = 0x0B
	RecL5Capability uint8 = 0x0F // L5 capability record (id = hash("capability:"+name))
	RecL6Trajectory uint8 = 0x0E // host-appended operation trajectory event
	RecVecCentroid  uint8 = 0xF0 // centroid vector for brute-force search
	// RecAgentRegistry marks an agent's registration record: idHash equals
	// the agentID itself and data carries the agent name JSON. One record
	// per agent, stored inside the agent's own domain; DeleteAgent
	// tombstones it together with the rest of the domain.
	RecAgentRegistry uint8 = 0x10
)

// DefaultAgentID is the implicit single-agent domain used by the api.Open
// compatibility path and legacy callers.
const DefaultAgentID uint64 = 0

func EncodeRecord(agentID uint64, recordType, flags uint8, idHash uint64, data []byte) []byte {
	buf := make([]byte, RecordHeaderSize+len(data))
	buf[0] = recordType
	buf[1] = flags
	binary.LittleEndian.PutUint32(buf[2:6], uint32(len(data)))
	binary.LittleEndian.PutUint64(buf[6:14], agentID)
	binary.LittleEndian.PutUint64(buf[14:22], idHash)
	copy(buf[RecordHeaderSize:], data)
	crc := crc32.ChecksumIEEE(buf[:22])
	crc = crc32.Update(crc, crc32.IEEETable, data)
	binary.LittleEndian.PutUint32(buf[22:26], crc)
	return buf
}

// RecordData decodes a record at offset into a GC-safe data copy. io.EOF at
// region end or zero-filled space; ErrCorruption on truncated header/body;
// ErrCRCMismatch on bad CRC (torn write).
func RecordData(mmap []byte, offset uint64) (recordType, flags uint8, data []byte, agentID, idHash uint64, err error) {
	off := int(offset)
	if off == len(mmap) {
		return 0, 0, nil, 0, 0, io.EOF
	}
	if off+RecordHeaderSize > len(mmap) {
		return 0, 0, nil, 0, 0, common.NewError(
			common.ErrCorruption,
			fmt.Sprintf("record header at offset %d exceeds file size %d", offset, len(mmap)),
		)
	}
	recordType = mmap[off]
	flags = mmap[off+1]
	dataLen := int(binary.LittleEndian.Uint32(mmap[off+2 : off+6]))
	agentID = binary.LittleEndian.Uint64(mmap[off+6 : off+14])
	idHash = binary.LittleEndian.Uint64(mmap[off+14 : off+22])
	if recordType == 0 && flags == 0 && dataLen == 0 && agentID == 0 && idHash == 0 {
		return 0, 0, nil, 0, 0, io.EOF
	}
	dataEnd := off + RecordHeaderSize + dataLen
	if dataEnd > len(mmap) {
		return 0, 0, nil, 0, 0, common.NewError(
			common.ErrCorruption,
			fmt.Sprintf("record at offset %d claims length %d but file ends at %d", offset, dataLen, len(mmap)),
		)
	}
	storedCRC := binary.LittleEndian.Uint32(mmap[off+22 : off+26])
	crc := crc32.ChecksumIEEE(mmap[off : off+22])
	crc = crc32.Update(crc, crc32.IEEETable, mmap[off+RecordHeaderSize:dataEnd])
	if crc != storedCRC {
		return 0, 0, nil, 0, 0, common.NewError(
			common.ErrCRCMismatch,
			fmt.Sprintf("record at offset %d failed CRC32 check", offset),
		)
	}
	data = make([]byte, dataLen)
	copy(data, mmap[off+RecordHeaderSize:dataEnd])
	return
}
