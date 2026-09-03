// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Engine read path: index lookups over the live record area.

package core

import (
	"github.com/qyiun666/MemHop/internal/common"
)

func (e *StorageEngine) ReadRecord(agentID, idHash uint64) (uint8, []byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return 0, nil, common.NewError(common.ErrClosed, "engine is closed")
	}
	offset, ok := e.index[agentID][idHash]
	if !ok {
		return 0, nil, common.NewError(common.ErrNotFound, "record not found")
	}
	rt, _, data, _, _, err := RecordData(e.mmap, offset)
	if err != nil {
		return 0, nil, err
	}
	return rt, data, nil
}
