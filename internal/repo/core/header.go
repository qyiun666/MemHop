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
// mcp/skill/composite resource-wrapper model; 0x0007 removed the L6 scene
// usage record (folded into SceneSlot), removed the L1 reverse index from
// the snapshot (L1 association now walks the scene hypergraph at query
// time) and added L1 hyperedge creation during Dream; 0x0008 added
// agent_id to the record frame (18 -> 26 bytes) so one file hosts
// multiple physically separated agent domains, and renumbered the
// trajectory layer down to L6 (RecL6Trajectory), converging the
// cognitive stack to L0-L6; 0x0009 re-shaped the L0 profile slot into
// typed distilled signals (EmotionState/MBTI) with explicit field
// ownership (host-authored identity vs distilled signals), removing the
// string-encoded emotion_patterns/lexicon/style_traits maps and the
// keyword-projection profile stage; 0x000A moved the L3 knowledge-graph
// records into a reserved file-wide shared domain (SharedPoolAgentID), so all
// agent domains in one file share a single L3 pool; 0x000B moved the L5
// capability records into that same shared pool (every agent domain uses one
// capability pool) and re-shaped the card into the uniform function-entry
// model — no card-level type or workflow, action chains live in a resource
// Config, and the memhop-capability/v4 document is a plugin package holding
// 1..N cards; 0x000C retired the L5 capability record layer entirely (the
// 0x0F frame type is gone): capabilities are host-owned memhop-capability/v4
// documents and the engine no longer stores them; 0x000D re-keyed L6 into one
// record space under one key — the topic id of the turn that produced the
// record — so a turn's trajectory events and the plan nodes it opened live
// together and a node's id is derived from that key. This moves record
// *meaning*, not layout: an older file's plan nodes carry a host-minted plan id
// where the key now goes, which addresses nothing under the new rule, so they
// cannot be read correctly. 0x000E moved a turn's content into one layer: an L4
// archive now carries either a dialogue original or an operation event (Kind),
// id'd by hash("l4:"+topic+":"+seq) so re-writing one Seq overwrites in place,
// and L6 keeps nothing but plan nodes, on the 0x0F frame type the retired L5
// capability record vacated. L4 became bounded with it — Dream drops content
// past the same 7-day window L6 already had, so a topic older than the window
// keeps its keyword track and nothing else. A 0x000D file stores its events in a
// record type that no longer exists and its archives under text-derived ids, so
// neither addresses anything under the new rule. 0x000F took the layer numbers
// out of the last two id namespaces that baked them in — a scene's L1 node hashes
// from "scene-node:"+sceneID and a content slot from "content:"+topic+":"+seq —
// so renaming a layer no longer re-keys records; renamed the archive's owning
// field to what it always held (context_id → topic_id); and dropped the scene's
// read-side counters (hit_count, last_hit_at, topic_count) along with the Dream
// stage that read them. A 0x000E file decodes its archive's owner into a field the
// new reader never fills, and its records sit under prefixes that no longer
// derive, so neither addresses anything under the new rule. 0x0010 re-shaped the
// plan layer's record and its writer at once: a plan node no longer carries a
// plan_type, `running` left the status vocabulary (in_progress is the one word for
// work under way), and a turn's tree is declared in one call instead of stepped
// through, so an event can no longer create the step it names. A 0x000F file may
// hold a node whose status is the retired value 4 — which the current vocabulary
// reports as an undefined stored status rather than guessing — and may hold events
// attributed to steps no declaration ever made, so neither reads correctly under
// the new rule. 0x0011 addressed plan nodes by a per-topic ordinal instead of a
// dotted path, cut the status vocabulary to three states with 0 meaning
// in_progress, and made a plan's tree grow node by node rather than in one
// declared restatement. A 0x0010 file stores its nodes under a path string this
// reader never consults, so every one of them arrives with seq 0 — not an address
// anything can be found under — and its status bytes carry the retired numbering,
// where 0 meant pending and 2 meant done, so a finished step reads as an
// in-progress one. 0x0012 put a domain's identity on its L0 profile
// (agent_type: the primary agent the file is opened on, or a sub agent) and gave
// a topic a name its host writes. A 0x0011 file's profiles carry no agent_type,
// so every domain in it decodes as the primary — not one wrong value somewhere
// but the same wrong value in every domain at once, against a rule that a file
// has exactly one of them. Nothing reading the file could tell that apart from a
// file that genuinely means it, which is why this one cannot be tolerated the way
// a missing optional field can. Files with 0x0011 (or older) are rejected at Open
// — there is no migration path.
const FormatVersion uint16 = 0x0012

var (
	Magic     = [4]byte{'M', 'E', 'H', '2'}
	TailMagic = [4]byte{'2', 'H', 'E', 'M'}
)

// FileHeader is the on-disk file header (4096 bytes).
// Layout: magic(4) version(2) reserved(2) commit_id(8) snapshot_off(8)
// snapshot_len(4) record_count(4) flags(4) record_end(8) reserved
// crc32(4) tail_magic(4).
// The bytes at offset 6 held the vector dimension until v1.5.0 retired the
// retrieval subsystem; nothing reads them now. New files write 0, files that
// carry a legacy value keep it as-is — the field takes part in neither the
// format version nor the A/B header choice (CRC + CommitID decide).
// RecordEnd is the end of the record area (start of the first tail
// snapshot). It is always written by 0x0005+ checkpoints; zero means
// "unknown" and Open reconstructs it with a one-time scan as a defensive
// measure against torn or hand-edited headers.
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
