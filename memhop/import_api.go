// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/query"
)

// ImportMemory imports data into the specified layer.
func (m *MemHop) ImportMemory(req query.ImportRequest) (*query.ImportResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	return query.ImportMemory(m.engine, m.sparseIndex, m.l3Index, m.l3Degree, m.l3Cache, req)
}
