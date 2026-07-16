package hash

import (
	"fmt"

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

// ParseID parses a 16-char hex string back to uint64.
func ParseID(id string) (uint64, error) {
	var h uint64
	_, err := fmt.Sscanf(id, "%016x", &h)
	return h, err
}
