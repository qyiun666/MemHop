// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 archive search operations of the internal layer.

package internal

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// L4Query archive query: the three modes are exclusive with priority
// Keyword > time range > IDs; TopicID filters in all modes.
type L4Query struct {
	Keyword string   `json:"keyword,omitempty"` // mode 1: content substring
	Start   int64    `json:"start,omitempty"`   // mode 2: time range [Start, End] (ms)
	End     int64    `json:"end,omitempty"`
	IDs     []string `json:"ids,omitempty"`      // mode 3: by id
	TopicID *string  `json:"topic_id,omitempty"` // extra: only archives of this topic
}

func (db *DB) SearchL4(agentID uint64, q L4Query) ([]core.ArchiveSlot, error) {
	ac, err := db.contextFor(agentID)
	if err != nil {
		return nil, err
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	var out []core.ArchiveSlot
	switch {
	case q.Keyword != "":
		out = repo.QueryArchiveL4(db.engine, agentID, 1, q.Keyword, 0, 0, nil)
	case q.Start > 0 && q.End > 0:
		out = repo.QueryArchiveL4(db.engine, agentID, 2, "", q.Start, q.End, nil)
	case len(q.IDs) > 0:
		out = repo.QueryArchiveL4(db.engine, agentID, 3, "", 0, 0, q.IDs)
	default:
		return []core.ArchiveSlot{}, nil
	}
	if q.TopicID != nil {
		topicHash, err := common.ParseID(*q.TopicID)
		if err != nil {
			return nil, common.NewError(common.ErrInvalidQuery, "parse topic id", err)
		}
		filtered := out[:0]
		for _, arc := range out {
			if arc.ContextID == topicHash {
				filtered = append(filtered, arc)
			}
		}
		out = filtered
	}
	if out == nil {
		return []core.ArchiveSlot{}, nil
	}
	return out, nil
}

func (db *DB) GetArchive(agentID uint64, id string) (*core.ArchiveSlot, error) {
	ac, err := db.contextFor(agentID)
	if err != nil {
		return nil, err
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	out := repo.QueryArchiveL4(db.engine, agentID, 3, "", 0, 0, []string{id})
	if len(out) == 0 {
		return nil, common.NewError(common.ErrNotFound, "archive not found")
	}
	return &out[0], nil
}
