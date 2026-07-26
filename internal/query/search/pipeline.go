// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Pipeline components: candidate set builder.

package search

import (
	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/core/index"
)

// BuildCandidateSet returns L2 IDs for scoped search.
//
// If l3ID is specified, restricts to L2 contexts referencing that L3 graph.
// Otherwise returns all depth≤2 L2 context IDs.
func BuildCandidateSet(
	l2Meta *index.L2MetaIndex,
	sparse *index.SparseIndex,
	l3ID *string,
) map[uint64]struct{} {
	if l3ID != nil {
		l3Hash, err := hash.ParseID(*l3ID)
		if err != nil {
			return nil
		}
		ids := l2Meta.GetL2IDsByL3(l3Hash)
		if len(ids) == 0 {
			return nil
		}
		set := make(map[uint64]struct{}, len(ids))
		for _, id := range ids {
			set[id] = struct{}{}
		}
		return set
	}
	// Full scan: all L2 IDs with depth ≤ 2.
	set := make(map[uint64]struct{})
	l2Meta.Iter(func(idHash uint64, meta *index.L2Meta) bool {
		if meta.Depth <= 2 {
			set[idHash] = struct{}{}
		}
		return true
	})
	if len(set) == 0 {
		return nil
	}
	return set
}
