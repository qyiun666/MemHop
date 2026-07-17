// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"memhop/internal/query/write"
	"memhop/internal/common/hash"
	"memhop/internal/common/mherrors"
)

// BatchStore runs the five-phase batch store pipeline.
func (m *MemHop) BatchStore(batch write.StoreBatch) (*write.StoreResult, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}

	deps := m.batchDeps()
	deps.L2Meta = m.l2Meta
	report, err := write.BatchStore(batch, deps)
	if err != nil {
		return nil, err
	}
	ids := makeItemIDs(batch.Items, report)
	return &write.StoreResult{
		StoredCount: report.L1NodesCreated + report.L4Docs,
		ItemIDs:     ids,
	}, nil
}

func makeItemIDs(items []write.StoreItem, report *write.BatchReport) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, hash.FormatHash(write.L1NodeIDHash(item.Content)))
	}
	return ids
}
