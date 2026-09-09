// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// L4 archive operations. AppendArchiveL4 stores one content record of a topic
// (ID = hash(contextID:createdAt:content)) and mirrors it into the domain's
// ArchiveIndex, which is how a topic's records are found again: the id hashes
// the text, so nothing derives it from the topic alone.
// QueryArchiveL4 reads by the same key or by num-style filters.

// ArchiveContent is one L4 write: the topic that owns the record, who spoke,
// the medium of Text and when it was said.
type ArchiveContent struct {
	TopicID   uint64
	Role      uint8
	Type      core.ContentType
	Text      string
	CreatedAt int64
}

func AppendArchiveL4(engine *core.StorageEngine, agentID uint64, idx *index.ArchiveIndex, in ArchiveContent) (uint64, error) {
	archiveID := common.HashID(fmt.Sprintf("%s:%d:%s", common.FormatHash(in.TopicID), in.CreatedAt, in.Text))
	arc := &core.ArchiveSlot{
		IDHash:      archiveID,
		ContentType: in.Type,
		Role:        in.Role,
		ContextID:   in.TopicID,
		CreatedAt:   in.CreatedAt,
		Content:     in.Text,
	}
	if err := core.WriteArchiveSlot(engine, agentID, archiveID, arc); err != nil {
		return 0, err
	}
	idx.Append(in.TopicID, archiveID, in.CreatedAt)
	return archiveID, nil
}

// DropArchivesL4 tombstones specific archives of one topic and mirrors the
// removal into idx. Missing IDs are skipped (DeleteRecordBatch is a
// best-effort tombstone pass), so a replay that already retired an id lands on
// the same state.
func DropArchivesL4(engine *core.StorageEngine, agentID uint64, idx *index.ArchiveIndex, topicID uint64, ids []uint64) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := engine.DeleteRecordBatch(agentID, ids); err != nil {
		return common.NewError(common.ErrIO, "delete l4 archives", err)
	}
	idx.RemoveIDs(topicID, ids)
	return nil
}

// DeleteTopicArchives tombstones every archive the index credits the given
// topics with, then drops those topics from the index. A topic with nothing
// indexed costs no write, and a topic being deleted never needs to hand over
// the ids it owned — the mirror is the list.
func DeleteTopicArchives(engine *core.StorageEngine, agentID uint64, idx *index.ArchiveIndex, topics []uint64) error {
	var doomed []uint64
	for _, topicID := range topics {
		doomed = append(doomed, idx.Hashes(topicID)...)
	}
	if len(doomed) > 0 {
		if _, err := engine.DeleteRecordBatch(agentID, common.DedupSorted(doomed)); err != nil {
			return common.NewError(common.ErrIO, "delete l4 archives", err)
		}
	}
	for _, topicID := range topics {
		idx.RemoveTopic(topicID)
	}
	return nil
}

// ArchiveQuery is the L4 read filter: every field is optional and the set
// conditions AND together, so an empty query selects the domain's whole
// archive set. Keyword is matched case-insensitively against a lower-cased
// query keyword (the L3 node filter matches the same way).
//
// Index lets a topic-scoped read go through the domain's archive cache instead
// of scanning the whole bucket; it only applies when TopicID is set.
type ArchiveQuery struct {
	IDs     []uint64
	TopicID *uint64
	Type    *core.ContentType
	Keyword string
	Start   int64
	End     int64
	Limit   int
	Index   *index.ArchiveIndex
}

// QueryArchivesL4 returns the archives matching every set condition, sorted by
// CreatedAt. An ID that names no record is skipped (a replayed Update legally
// retires the ids of the turn it replaced); a record that cannot be read is an
// error. A lookup that only names IDs takes the record-read fast path instead
// of scanning the domain. A topic-scoped lookup with an index reads exactly
// that topic's records, where an id the index names but the engine cannot read
// is an error rather than a shorter transcript. Limit keeps the newest
// matches, because the sort order is oldest first.
func QueryArchivesL4(engine *core.StorageEngine, agentID uint64, q ArchiveQuery) ([]core.ArchiveSlot, error) {
	q.Keyword = strings.ToLower(q.Keyword)
	if len(q.IDs) > 0 && q.TopicID == nil && q.Type == nil && q.Keyword == "" && q.Start == 0 && q.End == 0 {
		out, err := archivesByIDOnly(engine, agentID, q.IDs)
		if err != nil {
			return nil, err
		}
		if q.Limit <= 0 || len(out) <= q.Limit {
			return out, nil
		}
		slices.SortFunc(out, compareByCreatedAt)
		return newest(out, q.Limit), nil
	}
	if q.TopicID != nil && q.Index != nil {
		out, err := archivesByTopic(engine, agentID, q.Index.Hashes(*q.TopicID))
		if err != nil {
			return nil, err
		}
		filtered := out[:0]
		for _, arc := range out {
			if matchesArchiveQuery(arc, q) {
				filtered = append(filtered, arc)
			}
		}
		slices.SortFunc(filtered, compareByCreatedAt)
		return newest(filtered, q.Limit), nil
	}
	var out []core.ArchiveSlot
	for _, arc := range core.CollectAllArchives(engine, agentID) {
		if matchesArchiveQuery(arc, q) {
			out = append(out, arc)
		}
	}
	slices.SortFunc(out, compareByCreatedAt)
	return newest(out, q.Limit), nil
}

func compareByCreatedAt(a, b core.ArchiveSlot) int {
	return cmp.Compare(a.CreatedAt, b.CreatedAt)
}

// newest keeps the last limit entries of a CreatedAt-ascending result.
func newest(out []core.ArchiveSlot, limit int) []core.ArchiveSlot {
	if limit <= 0 || len(out) <= limit {
		return out
	}
	return out[len(out)-limit:]
}

func archivesByIDOnly(engine *core.StorageEngine, agentID uint64, ids []uint64) ([]core.ArchiveSlot, error) {
	var out []core.ArchiveSlot
	for _, idHash := range ids {
		arc, err := core.ReadArchiveSlot(engine, agentID, idHash)
		if err != nil {
			if common.CodeOf(err) == common.ErrNotFound {
				continue
			}
			return nil, err
		}
		out = append(out, *arc)
	}
	return out, nil
}

// archivesByTopic reads the records one topic's archive index names. A record
// that is gone is an error, not a skip: the index is what the topic owns, so
// an entry naming nothing means the mirror and the disk disagree, and a
// transcript silently missing one utterance reads exactly like a complete one.
func archivesByTopic(engine *core.StorageEngine, agentID uint64, ids []uint64) ([]core.ArchiveSlot, error) {
	out := make([]core.ArchiveSlot, 0, len(ids))
	for _, idHash := range ids {
		arc, err := core.ReadArchiveSlot(engine, agentID, idHash)
		if err != nil {
			if common.CodeOf(err) == common.ErrNotFound {
				return nil, common.NewError(common.ErrIO, "archive index names a missing record", err)
			}
			return nil, err
		}
		out = append(out, *arc)
	}
	return out, nil
}

func matchesArchiveQuery(arc core.ArchiveSlot, q ArchiveQuery) bool {
	if len(q.IDs) > 0 && !slices.Contains(q.IDs, arc.IDHash) {
		return false
	}
	if q.TopicID != nil && arc.ContextID != *q.TopicID {
		return false
	}
	if q.Type != nil && arc.ContentType != *q.Type {
		return false
	}
	if q.Keyword != "" && !strings.Contains(strings.ToLower(arc.Content), q.Keyword) {
		return false
	}
	if q.Start > 0 && arc.CreatedAt < q.Start {
		return false
	}
	if q.End > 0 && arc.CreatedAt > q.End {
		return false
	}
	return true
}
