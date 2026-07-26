// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// 批量写入与导入
package memhop

import (
	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/query/importx"
	"github.com/qyiun666/MemHop/internal/query/write"
)

// BatchStore runs the five-phase batch store pipeline.
//
// Every StoreItem must carry non-empty Keywords (pre-extracted facts/terms);
// items without keywords fail the whole batch with an error. The result
// reports one status per input item, flagging deduplicated items.
func (m *MemHop) BatchStore(batch write.StoreBatch) (*write.StoreResult, error) {
	if err := m.beginRead(); err != nil {
		return nil, err
	}
	defer m.mu.RUnlock()

	deps := m.batchDeps()
	deps.L2Meta = m.getL2Meta()
	report, err := write.BatchStore(batch, deps)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(report.Items))
	items := make([]write.StoreItemStatus, len(report.Items))
	for i, oc := range report.Items {
		ids[i] = hash.FormatHash(oc.NodeID)
		items[i] = write.StoreItemStatus{ID: ids[i], Dedup: oc.Dedup}
	}
	return &write.StoreResult{
		StoredCount: report.L1NodesCreated + report.L4Docs,
		ItemIDs:     ids,
		Items:       items,
	}, nil
}

// ImportMemory imports data into the specified layer.
func (m *MemHop) ImportMemory(req importx.ImportRequest) (*importx.ImportResult, error) {
	if err := m.beginRead(); err != nil {
		return nil, err
	}
	defer m.mu.RUnlock()
	return importx.ImportMemory(m.engine, m.sparseIndex, m.getL2Meta(), m.l3Index, m.l3Degree, m.l3Cache, req)
}
