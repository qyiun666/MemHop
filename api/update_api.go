// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"memhop/internal/common/mherrors"
	"memhop/internal/query/crud"
	"memhop/internal/query/write"
)

// UpdateMemory updates a memory item at the specified layer.
func (m *MemHop) UpdateMemory(req crud.UpdateRequest) (*crud.UpdateResult, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	deps := &write.UpdateDeps{
		Engine:        m.engine,
		SparseIndex:   m.sparseIndex,
		LlmCfg:        &m.config.LLM,
		PreprocessCfg: m.defaults.LlmPreprocess,
	}
	return write.UpdateMemory(req, deps)
}
