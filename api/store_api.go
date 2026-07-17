// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"memhop/internal/core"
	"memhop/internal/core/query"
	"memhop/internal/hash"
)

// BatchStore runs the five-phase batch store pipeline.
func (m *MemHop) BatchStore(batch query.StoreBatch) (*query.StoreResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, core.ErrClosed
	}

	deps := m.batchDeps()
	deps.L2Meta = m.l2Meta
	report, err := query.BatchStore(batch, deps)
	if err != nil {
		return nil, err
	}
	ids := makeItemIDs(batch.Items, report)
	return &query.StoreResult{
		StoredCount: report.L1NodesCreated + report.L4Docs,
		ItemIDs:     ids,
	}, nil
}

func makeItemIDs(items []query.StoreItem, report *query.BatchReport) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, hash.FormatHash(query.L1NodeIDHash(item.Content)))
	}
	return ids
}
