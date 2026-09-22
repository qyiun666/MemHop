// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 archive operations of the internal layer: one write and one read over a
// topic's content, which is where a turn's dialogue originals and its operation
// events both live.

package internal

import (
	"fmt"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/content"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// SearchL4 reads the content records matching every condition of q; the
// conditions AND together, so an empty query returns the domain's whole content
// set — utterances and events alike (Kind is one of the conditions). Keyword is
// case-insensitive. Limit keeps the tail of whatever order the read is in: the
// newest matches across topics, the highest slots inside one. NodeSeq keeps only
// the work of one plan step and its whole subtree (splitting a step moves its
// work onto the children), and is refused without TopicID. Zero leaves a
// condition unset. A Kind or Type outside the vocabulary is refused: the write
// boundary rejects the same values, and matching nothing would read back as
// "this turn holds none".
func (db *DB) SearchL4(agentID uint64, q L4Query) ([]core.ArchiveSlot, error) {
	ac, err := db.lockAgent(agentID)
	if err != nil {
		return nil, err
	}
	defer ac.Mu.Unlock()
	if q.NodeSeq != 0 && q.TopicID == nil {
		return nil, common.NewError(common.ErrInvalidQuery,
			"a step filter needs its turn's topic id")
	}
	if q.Kind != nil && !q.Kind.Valid() {
		return nil, common.NewError(common.ErrInvalidQuery, "unknown archive kind")
	}
	if q.Type != nil && !q.Type.Valid() {
		return nil, common.NewError(common.ErrInvalidQuery, "unknown content type")
	}
	rq := repo.ArchiveQuery{Keyword: q.Keyword, Start: q.Start, End: q.End, Type: q.Type,
		Kind: q.Kind, Limit: q.Limit, Index: ac.L4}
	if len(q.IDs) > 0 {
		ids, err := common.ParseAll(q.IDs)
		if err != nil {
			return nil, common.NewError(common.ErrInvalidQuery, "parse archive ids", err)
		}
		rq.IDs = ids
	}
	if q.TopicID != nil {
		topicHash, err := content.ParseTopicID(*q.TopicID)
		if err != nil {
			return nil, err
		}
		rq.TopicID = &topicHash
		if q.NodeSeq != 0 {
			// The whole branch is expanded here so the data layer only ever
			// matches set membership: it has no view of the tree.
			rq.NodeSeqs = ac.Plans.Subtree(topicHash, q.NodeSeq)
		}
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

// AppendArchive writes one piece of the open turn's content and returns the slot it
// took. Kind says which track it belongs to: what somebody said, or what happened
// while they said it. The turn is the one Search opened for this domain, so a host
// recording the work of its own round names no ids — and the pair it would have
// named cannot then disagree with the library's own record of which turn is open.
//
// Seq 0 allocates a slot above everything the topic holds, above Seq 1 and 2 which
// belong to the turn's dialogue; the slot taken is what this call returns, and
// naming that slot again is an overwrite, not an error — that is what lets a
// replayed append converge instead of accumulating versions. An event may name the
// plan step it belongs to (NodeSeq), which has to exist already: the tree is what
// the plan write face creates, so an ordinal nobody created is refused, not grown.
// A record that does not satisfy the write contract is refused before anything is
// stored.
func (db *DB) AppendArchive(agentID uint64, slot core.ArchiveSlot) (uint64, error) {
	ac, err := db.lockTurn(agentID)
	if err != nil {
		return 0, err
	}
	defer ac.Mu.Unlock()
	if slot.Kind == core.KindEvent && slot.NodeSeq != 0 &&
		!ac.Plans.HasSeq(ac.Turn, slot.NodeSeq) {
		return 0, common.NewError(common.ErrInvalidQuery,
			fmt.Sprintf("the event names step %d, which is not on this turn's plan tree",
				slot.NodeSeq))
	}
	return content.Append(ac, agentID, ac.Turn, slot)
}
