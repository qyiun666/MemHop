// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// AppendL4Message appends one message to an existing topic: pure storage
// append — no keyword extraction, no LLM call. The new record id is
// appended to the topic's L4Refs (append + DedupSorted). role must be one
// of core.RoleUser / core.RoleAgent (RoleSystem / RoleDream also defined);
// undefined values are rejected. Returns the new L4 record id (uint64
// hash); hosts format it with common.FormatHash.
func (db *DB) AppendL4Message(topicID string, text string, timestamp int64, role uint8) (uint64, error) {
	if err := db.beginRead(); err != nil {
		return 0, err
	}
	defer db.mu.RUnlock()
	if text == "" || timestamp <= 0 {
		return 0, common.NewError(common.ErrInvalidQuery, "AppendL4Message requires text and a positive timestamp")
	}
	if role > core.RoleDream {
		return 0, common.NewError(common.ErrInvalidQuery, "AppendL4Message: undefined role")
	}
	if _, err := common.ParseID(topicID); err != nil {
		return 0, err
	}
	// Validate the topic before any write: a missing topic must not leave
	// an orphan L4 archive behind (same guard as Update).
	topics, err := repo.ListTopicsL2(repo.TopicListQuery{
		Engine:  db.engine,
		MetaIdx: db.l2Meta,
		SceneID: topicID,
		Num:     3,
	})
	if err != nil {
		return 0, err
	}
	if len(topics) == 0 {
		return 0, common.NewError(common.ErrNotFound, "topic not found")
	}
	archiveID, err := repo.AppendArchiveL4(db.engine, core.DefaultAgentID, topicID, role, core.ContentText, text, timestamp)
	if err != nil {
		return 0, err
	}
	if !repo.UpdateTopicL4RefsL2(db.engine, core.DefaultAgentID, topicID, []uint64{archiveID}) {
		return 0, common.NewError(common.ErrIO, "update topic l4 ref", nil)
	}
	return archiveID, nil
}
