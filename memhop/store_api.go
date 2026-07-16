// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/query"
	"github.com/qyiun666/memhop/memhop/internal/hash"
)

// BatchStore runs the five-phase batch store pipeline.
func (m *MemHop) BatchStore(batch query.StoreBatch) (*query.StoreResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, core.ErrClosed
	}

	report, err := query.BatchStore(batch, m.batchDeps())
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
		ids = append(ids, hash.FormatHash(hash.HashID(item.Content)))
	}
	return ids
}
