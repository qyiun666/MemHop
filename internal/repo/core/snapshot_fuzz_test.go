// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"math"
	"reflect"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

// FuzzSnapshot exercises arbitrary decoder input and proves that every index
// BuildSnapshot emits parses back to the same live domains and entries. Empty
// domains are intentionally absent from the serialized form.
func FuzzSnapshot(f *testing.F) {
	valid := BuildSnapshot(map[uint64]map[uint64]uint64{
		DefaultAgentID: {1: DataStart, math.MaxUint64: math.MaxUint64},
		42:             {7: 123456},
	})
	badCRC := bytes.Clone(valid)
	badCRC[len(badCRC)-1] ^= 0xFF
	truncatedAgent := BuildSnapshot(nil)
	binary.LittleEndian.PutUint32(truncatedAgent[5:9], 1)
	binary.LittleEndian.PutUint32(truncatedAgent[len(truncatedAgent)-4:], crc32.ChecksumIEEE(truncatedAgent[:len(truncatedAgent)-4]))
	for _, seed := range [][]byte{valid, badCRC, truncatedAgent, nil, make([]byte, 12), []byte("not a snapshot")} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		index := map[uint64]map[uint64]uint64{
			DefaultAgentID:    {0: 0, math.MaxUint64: math.MaxUint64},
			SharedPoolAgentID: {},
		}
		for i, b := range raw[:min(len(raw), 16)] {
			agentID := uint64(i%3) + 1
			if index[agentID] == nil {
				index[agentID] = map[uint64]uint64{}
			}
			index[agentID][uint64(i)<<8|uint64(b)] = uint64(i)*DataStart + uint64(b)
		}
		want := make(map[uint64]map[uint64]uint64, len(index))
		for agentID, entries := range index {
			if len(entries) != 0 {
				want[agentID] = entries
			}
		}
		got, err := ParseSnapshot(BuildSnapshot(index))
		if err != nil {
			t.Fatalf("valid snapshot did not parse: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("snapshot round trip changed index: got=%v want=%v", got, want)
		}

		parsed, err := ParseSnapshot(raw)
		if err != nil {
			if common.CodeOf(err) == 0 {
				t.Fatalf("snapshot error has no numeric code: %v", err)
			}
			return
		}
		roundTripped, err := ParseSnapshot(BuildSnapshot(parsed))
		if err != nil {
			t.Fatalf("accepted snapshot could not be encoded again: %v", err)
		}
		if !reflect.DeepEqual(roundTripped, parsed) {
			t.Fatalf("accepted snapshot changed on re-encoding: got=%v want=%v", roundTripped, parsed)
		}
	})
}
