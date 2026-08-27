// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Engine read path: index lookups and the raw-frame scan that recovers
// pre-delete payloads.

package core

import (
	"errors"
	"io"

	"github.com/qyiun666/MemHop/internal/common"
)

func (e *StorageEngine) ReadRecord(agentID, idHash uint64) (uint8, []byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return 0, nil, common.NewError(common.ErrClosed, "engine is closed")
	}
	offset, ok := e.index[agentID][idHash]
	if !ok {
		return 0, nil, common.NewError(common.ErrNotFound, "record not found")
	}
	rt, _, data, _, _, err := RecordData(e.mmap, offset)
	if err != nil {
		return 0, nil, err
	}
	return rt, data, nil
}

// ScanDeletedPayloads returns, for every record of the given type in the
// agent domain whose newest frame is a tombstone, the payload of the
// newest non-tombstone frame (the pre-delete value). Frames already
// reclaimed or compacted are gone forever and cannot be recovered.
// Payloads are copied so callers may write them back without holding the
// mmap view.
func (e *StorageEngine) ScanDeletedPayloads(agentID uint64, recordType uint8) (map[uint64][]byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return nil, common.NewError(common.ErrClosed, "engine is closed")
	}
	seen, payload, err := e.scanDeletedFrames(agentID, recordType)
	if err != nil {
		return nil, err
	}
	out := make(map[uint64][]byte)
	for id, del := range seen {
		if del {
			if p, ok := payload[id]; ok {
				out[id] = p
			}
		}
	}
	return out, nil
}

// scanDeletedFrames walks the live record area classifying the newest
// frame per (agent, type) as live or tombstoned. Stops at crash residue
// (EOF or a CRC-mismatched frame). Caller must hold at least e.mu.RLock.
func (e *StorageEngine) scanDeletedFrames(agentID uint64, recordType uint8) (map[uint64]bool, map[uint64][]byte, error) {
	payload := make(map[uint64][]byte)
	deleted := make(map[uint64]bool)
	offset := uint64(DataStart)
	for offset < e.nextOffset {
		rt, flags, data, recAgent, idHash, err := RecordData(e.mmap, offset)
		if err != nil {
			if errors.Is(err, io.EOF) || common.CodeOf(err) == common.ErrCRCMismatch {
				break // crash residue after the last clean frame
			}
			return nil, nil, err
		}
		if recAgent == agentID && rt == recordType {
			if flags&FlagDeleted != 0 {
				deleted[idHash] = true
			} else {
				deleted[idHash] = false
				payload[idHash] = append([]byte(nil), data...)
			}
		}
		offset += uint64(RecordHeaderSize) + uint64(len(data))
	}
	return deleted, payload, nil
}
