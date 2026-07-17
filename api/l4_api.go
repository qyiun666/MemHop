// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop

import (
	"encoding/json"

	"memhop/internal/core"
	"memhop/internal/core/model"
	"memhop/internal/core/query"
	"memhop/internal/core/storage"
	"memhop/internal/hash"
)

// QueryArchives searches L4 archives with filters.
func (m *MemHop) QueryArchives(q query.ArchiveQuery) (*query.ArchiveListResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	return query.QueryArchives(m.engine, q)
}

// GetArchive loads a single archive by hex ID.
func (m *MemHop) GetArchive(id string) (*query.Archive, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, core.ErrClosed
	}
	idHash, err := hash.ParseID(id)
	if err != nil {
		return nil, err
	}
	rt, data, err := m.engine.ReadRecord(idHash)
	if err != nil {
		return nil, err
	}
	if rt != storage.RecL4Archive {
		return nil, nil
	}
	var slot model.ArchiveSlot
	if json.Unmarshal(data, &slot) != nil {
		return nil, nil
	}
	topicID := hash.FormatHash(slot.ContextID)
	return &query.Archive{
		ID:          hash.FormatHash(slot.IDHash),
		Content:     slot.Content,
		ContentType: slot.ContentType.String(),
		TopicID:     &topicID,
		EngramIDs:   []string{},
		CreatedAt:   slot.CreatedAt,
	}, nil
}
