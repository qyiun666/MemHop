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
func AppendArchiveL4(engine *core.StorageEngine, contextID string, role uint8, contentType core.ContentType, content string, createdAt int64) (uint64, error) {
	ctxHash, err := common.ParseID(contextID)
	if err != nil {
		return 0, common.NewError(common.ErrInvalidQuery, "parse context id", err)
	}
	archiveID := common.HashID(fmt.Sprintf("%s:%d:%s", contextID, createdAt, content))
	arc := &core.ArchiveSlot{
		IDHash:      archiveID,
		ContentType: contentType,
		Role:        role,
		ContextID:   ctxHash,
		CreatedAt:   createdAt,
		Content:     content,
	}
	if err := core.WriteArchiveSlot(engine, archiveID, arc); err != nil {
		return 0, err
	}
	return archiveID, nil
}

// QueryArchiveL4 queries archives: num==1 keyword substring match, num==2
// time range [start, end] sorted by CreatedAt, num==3 by id (missing skipped).
func QueryArchiveL4(engine *core.StorageEngine, num uint8, keyword string, start, end int64, ids []string) []core.ArchiveSlot {
	switch num {
	case 1: // keyword
		var out []core.ArchiveSlot
		for _, arc := range core.CollectAllArchives(engine) {
			if strings.Contains(arc.Content, keyword) {
				out = append(out, arc)
			}
		}
		return out
	case 2: // time range
		var out []core.ArchiveSlot
		for _, arc := range core.CollectAllArchives(engine) {
			if arc.CreatedAt >= start && arc.CreatedAt <= end {
				out = append(out, arc)
			}
		}
		slices.SortFunc(out, func(a, b core.ArchiveSlot) int {
			return cmp.Compare(a.CreatedAt, b.CreatedAt)
		})
		return out
	case 3: // by id
		var out []core.ArchiveSlot
		for _, id := range ids {
			idHash, err := common.ParseID(id)
			if err != nil {
				continue
			}
			arc, err := core.ReadArchiveSlot(engine, idHash)
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
