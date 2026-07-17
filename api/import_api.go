// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"memhop/internal/query/importx"
	"memhop/internal/common/mherrors"
)

// ImportMemory imports data into the specified layer.
func (m *MemHop) ImportMemory(req importx.ImportRequest) (*importx.ImportResult, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	return importx.ImportMemory(m.engine, m.sparseIndex, m.l3Index, m.l3Degree, m.l3Cache, req)
}
