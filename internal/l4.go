// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 archive operations of the internal layer: one write and one read over a
// topic's content, which is where a turn's dialogue originals and its operation
// events both live.

package internal

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/content"
	"github.com/qyiun666/MemHop/internal/plan"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// SearchL4 reads the content records matching every condition of q; the
// conditions AND together, so an empty query returns the domain's whole content
// set — utterances AND events alike, which is why Kind is one of the conditions.
// Keyword is case-insensitive and Limit keeps the newest matches.
func (db *DB) SearchL4(agentID uint64, q L4Query) ([]core.ArchiveSlot, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	rq := repo.ArchiveQuery{Keyword: q.Keyword, Start: q.Start, End: q.End, Type: q.Type,
		Kind: q.Kind, Limit: q.Limit, Index: ac.L4}
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

// AppendArchive writes one piece of a turn's content under topicID, the key Search
// issued for that turn. Kind says which track it belongs to: what somebody said,
// or what happened while they said it.
//
// Seq 0 allocates a slot above everything the topic holds already, including the
// two kept for dialogue, so the host never counts sequences. A Seq named
// explicitly writes that slot and taking one already held is an overwrite, not an
// error — that is what lets a replayed append converge instead of accumulating
// versions.
//
// An event whose NodePath names a plan step hangs on it, and a step missing along
// that dotted path is created as pending: this is also how a host adds a step to
// the turn's plan. A record that does not satisfy the write contract is refused
// before anything is stored, including before a node is created or advanced.
func (db *DB) AppendArchive(agentID uint64, topicID string, slot core.ArchiveSlot) error {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return err
	}
	defer ac.Mu.Unlock()
	th, err := content.ParseTopicID(topicID)
	if err != nil {
		return err
	}
	// Checked before the tree moves: a refused record must not leave a node chain
	// created behind it.
	if err := content.ValidateAppend(slot); err != nil {
		return err
	}
	if slot.Kind == core.KindEvent && slot.NodePath != "" {
		if _, err := plan.EnsureNode(ac, agentID, th, slot.NodePath); err != nil {
			return err
		}
	}
	_, err = content.Append(ac, agentID, th, slot)
	return err
}
