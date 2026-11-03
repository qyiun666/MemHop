// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"bytes"
	"errors"
	"io"
	"math"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

// FuzzFrameRecord checks both directions of the frame contract: bytes produced by
// EncodeRecord survive RecordData unchanged, and no byte slice or offset can make
// the decoder panic or fail without a numeric error code. io.EOF remains the
// scanner's sentinel for a region boundary or zero-filled space.
func FuzzFrameRecord(f *testing.F) {
	valid := EncodeRecord(42, RecL4Archive, FlagDeleted, 99, []byte("payload"))
	rotted := bytes.Clone(valid)
	rotted[RecordHeaderSize] ^= 0xFF
	for _, seed := range []struct {
		raw    []byte
		offset uint64
	}{
		{valid, 0},
		{valid, uint64(len(valid))},
		{valid, uint64(len(valid) + 1)},
		{valid, math.MaxUint64},
		{rotted, 0},
		{nil, 0},
		{make([]byte, RecordHeaderSize-1), 0},
		{make([]byte, RecordHeaderSize), 0},
	} {
		f.Add(seed.raw, seed.offset)
	}

	f.Fuzz(func(t *testing.T, raw []byte, offset uint64) {
		payload := bytes.Clone(raw)
		frame := EncodeRecord(42, RecL4Archive, FlagDeleted, 99, payload)
		recordType, flags, data, agentID, idHash, err := RecordData(frame, 0)
		if err != nil {
			t.Fatalf("valid frame did not decode: %v", err)
		}
		if recordType != RecL4Archive || flags != FlagDeleted || agentID != 42 || idHash != 99 || !bytes.Equal(data, payload) {
			t.Fatalf("frame round trip changed fields: type=%d flags=%d agent=%d id=%d data=%q want=%q",
				recordType, flags, agentID, idHash, data, payload)
		}

		_, _, _, _, _, err = RecordData(raw, offset)
		if err == nil || errors.Is(err, io.EOF) {
			return
		}
		if common.CodeOf(err) == 0 {
			t.Fatalf("decoder error has no numeric code: %v", err)
		}
	})
}
