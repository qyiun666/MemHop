package hash

import (
	"fmt"
	"strconv"

	"github.com/cespare/xxhash/v2"
)

// HashID computes xxhash64 of a string, compatible with Rust twox-hash (seed 0).
func HashID(s string) uint64 {
	return xxhash.Sum64String(s)
}

// FormatHash formats a uint64 as a 16-char hex string.
func FormatHash(h uint64) string {
	return fmt.Sprintf("%016x", h)
}

// ParseID parses a 16-char lowercase/uppercase hex string back to uint64.
// Malformed IDs (wrong length or non-hex characters) return an error.
func ParseID(id string) (uint64, error) {
	if len(id) != 16 {
		return 0, fmt.Errorf("invalid id %q: want exactly 16 hex chars", id)
	}
	h, err := strconv.ParseUint(id, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid id %q: %w", id, err)
	}
	return h, nil
}

// FormatIDs formats a slice of uint64 as hex strings.
func FormatIDs(ids []uint64) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = FormatHash(id)
	}
	return out
}
