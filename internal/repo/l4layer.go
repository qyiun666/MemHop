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
)

// L4 archive operations: AppendArchiveL4 stores a message (ID =
// hash(contextID:createdAt:content)); QueryArchiveL4 queries by num
// (1=keyword, 2=time range, 3=by id).
func AppendArchiveL4(engine *core.StorageEngine, agentID uint64, contextID uint64, role uint8, contentType core.ContentType, content string, createdAt int64) (uint64, error) {
	archiveID := common.HashID(fmt.Sprintf("%s:%d:%s", common.FormatHash(contextID), createdAt, content))
	arc := &core.ArchiveSlot{
		IDHash:      archiveID,
		ContentType: contentType,
		Role:        role,
		ContextID:   contextID,
		CreatedAt:   createdAt,
		Content:     content,
	}
	if err := core.WriteArchiveSlot(engine, agentID, archiveID, arc); err != nil {
		return 0, err
	}
	return archiveID, nil
}

// DeleteArchivesL4 batch-deletes archive records by ID; missing IDs are
// skipped (DeleteRecordBatch is a best-effort tombstone pass).
func DeleteArchivesL4(engine *core.StorageEngine, agentID uint64, ids []uint64) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := engine.DeleteRecordBatch(agentID, ids); err != nil {
		return common.NewError(common.ErrIO, "delete l4 archives", err)
	}
	return nil
}

// ArchiveQuery is the L4 read filter: every field is optional and the set
// conditions AND together, so an empty query selects the domain's whole
// archive set. Keyword is matched case-insensitively against a lower-cased
// query keyword (the L3 node filter matches the same way).
type ArchiveQuery struct {
	IDs     []uint64
	TopicID *uint64
	Type    *core.ContentType
	Keyword string
	Start   int64
	End     int64
	Limit   int
}

// QueryArchivesL4 returns the archives matching every set condition, sorted by
// CreatedAt. An ID that names no record is skipped (a replayed Update legally
// retires the ids of the turn it replaced); a record that cannot be read is an
// error. A lookup that only names IDs takes the record-read fast path instead
// of scanning the domain. Limit keeps the newest matches, because the sort
// order is oldest first.
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
