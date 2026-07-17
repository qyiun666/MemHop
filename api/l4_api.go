// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"memhop/internal/query/crud"
	"memhop/internal/common/mherrors"
)

// QueryArchives searches L4 archives with filters.
func (m *MemHop) QueryArchives(q crud.ArchiveQuery) (*crud.ArchiveListResult, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	return crud.QueryArchives(m.engine, q)
}

// GetArchive loads a single archive by hex ID.
func (m *MemHop) GetArchive(id string) (*crud.Archive, error) {
	if m.closed.Load() {
		return nil, mherrors.ErrClosed
	}
	return crud.GetArchive(m.engine, id)
}
