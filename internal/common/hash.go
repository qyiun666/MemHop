// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package common

import (
	"fmt"
	"strconv"

	"github.com/cespare/xxhash/v2"
)

// HashID computes xxhash64 of a string, compatible with Rust twox-hash (seed 0).
func HashID(s string) uint64 {
	return xxhash.Sum64String(s)
}

func FormatHash(h uint64) string {
	const digits = "0123456789abcdef"
	var buf [16]byte
	for i := 15; i >= 0; i-- {
		buf[i] = digits[h&0xf]
		h >>= 4
	}
	return string(buf[:])
}

// ParseID parses a 16-char hex string back to uint64; malformed IDs return an error.
func ParseID(id string) (uint64, error) {
	if len(id) != 16 {
		return 0, NewError(ErrInvalidQuery, fmt.Sprintf("invalid id %q: want exactly 16 hex chars", id))
	}
	h, err := strconv.ParseUint(id, 16, 64)
	if err != nil {
		return 0, NewError(ErrInvalidQuery, fmt.Sprintf("invalid id %q", id), err)
	}
	return h, nil
}

// ParseAll parses id strings into hashes; any malformed id fails the whole call.
func ParseAll(ids []string) ([]uint64, bool) {
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		h, err := ParseID(id)
		if err != nil {
			return nil, false
		}
		out = append(out, h)
	}
	return out, true
}
