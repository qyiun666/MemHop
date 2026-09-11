// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Engine read path: index lookups over the live record area.

package core

import (
	"errors"
	"io"

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
		if errors.Is(err, io.EOF) {
			// io.EOF is the frame scanner's "stop here", which means nothing to a
			// caller that named one id: an entry pointing at the end of the log or
			// at zero-filled space is the index disagreeing with the record area,
			// and it has to carry a code — an error code 0 reads as success.
			return 0, nil, common.NewError(common.ErrCorruption,
				"the index names a record the record area does not hold", err)
		}
		return 0, nil, err
	}
	return rt, data, nil
}
