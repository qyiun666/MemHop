// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package common

import (
	"slices"
)

// DedupSorted sorts ascending and dedups via the stdlib slices pipeline
// (Compact requires sorted input).
func DedupSorted(ids []uint64) []uint64 {
	return slices.Compact(slices.Sorted(slices.Values(ids)))
}

func ToSet(ids []uint64) map[uint64]struct{} {
	s := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		s[id] = struct{}{}
	}
	return s
}

// RemoveOnce removes the first occurrence of v from s (no-op when absent).
func RemoveOnce[T comparable](s []T, v T) []T {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

// Union returns a ++ b with duplicates removed, keeping first-seen order.
func Union[T comparable](a, b []T) []T {
	out := make([]T, 0, len(a)+len(b))
	out = append(out, a...)
	seen := make(map[T]struct{}, len(out))
	for _, x := range out {
		seen[x] = struct{}{}
	}
	for _, x := range b {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}
