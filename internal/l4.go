// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 archive search operations of the internal layer.

package internal

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// SearchL4 reads the archives matching every condition of q; the conditions AND
// together, so an empty query returns the domain's whole archive set. Keyword is
// case-insensitive and Limit keeps the newest matches.
func (db *DB) SearchL4(agentID uint64, q L4Query) ([]core.ArchiveSlot, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	rq := repo.ArchiveQuery{Keyword: q.Keyword, Start: q.Start, End: q.End, Type: q.Type, Limit: q.Limit}
	if len(q.IDs) > 0 {
		ids, ok := common.ParseAll(q.IDs)
		if !ok {
			return nil, common.NewError(common.ErrInvalidQuery, "parse archive ids")
		}
		rq.IDs = ids
	}
	if q.TopicID != nil {
		topicHash, err := common.ParseID(*q.TopicID)
		if err != nil {
			return nil, common.NewError(common.ErrInvalidQuery, "parse topic id", err)
		}
		rq.TopicID = &topicHash
	}
	out, err := repo.QueryArchivesL4(db.engine, agentID, rq)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return []core.ArchiveSlot{}, nil
	}
	return out, nil
}
