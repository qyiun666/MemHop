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

// Archive query modes of QueryArchiveL4.
const (
	ArchiveByKeyword uint8 = iota + 1 // substring match on Content
	ArchiveByTime                     // created within [start, end], sorted
	ArchiveByID                       // by id, missing ids skipped
)

// QueryArchiveL4 queries archives by the given mode: ArchiveByKeyword
// (substring match), ArchiveByTime (range [start, end] sorted by CreatedAt) or
// ArchiveByID (missing ids skipped).
func QueryArchiveL4(engine *core.StorageEngine, agentID uint64, mode uint8, keyword string, start, end int64, ids []uint64) []core.ArchiveSlot {
	switch mode {
	case ArchiveByKeyword:
		var out []core.ArchiveSlot
		for _, arc := range core.CollectAllArchives(engine, agentID) {
			if strings.Contains(arc.Content, keyword) {
				out = append(out, arc)
			}
		}
		return out
	case ArchiveByTime:
		var out []core.ArchiveSlot
		for _, arc := range core.CollectAllArchives(engine, agentID) {
			if arc.CreatedAt >= start && arc.CreatedAt <= end {
				out = append(out, arc)
			}
		}
		slices.SortFunc(out, func(a, b core.ArchiveSlot) int {
			return cmp.Compare(a.CreatedAt, b.CreatedAt)
		})
		return out
	case ArchiveByID:
		var out []core.ArchiveSlot
		for _, idHash := range ids {
			arc, err := core.ReadArchiveSlot(engine, agentID, idHash)
			if err != nil {
				continue
			}
			out = append(out, *arc)
		}
		return out
	default:
		return nil
	}
}
