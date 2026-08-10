// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// L4 对话原文操作：search/update 时追加消息返回 id；查询按 num 枚举
// （1=关键词、2=时间范围、3=按 id），统一返回 []ArchiveSlot。
// AppendArchiveL4 追加一条对话原文，ID = hash(contextID:createdAt:content)，返回消息 ID。
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

// QueryArchiveL4 查询对话原文。num==1 关键词子串匹配；num==2 时间范围
// [start, end]（按 CreatedAt 升序）；num==3 按 id 读取（跳过不存在的）。
// 其他 num 返回 nil。
func QueryArchiveL4(engine *core.StorageEngine, num uint8, keyword string, start, end int64, ids []string) []core.ArchiveSlot {
	switch num {
	case 1: // 关键词
		var out []core.ArchiveSlot
		for _, arc := range core.CollectAllArchives(engine) {
			if strings.Contains(arc.Content, keyword) {
				out = append(out, arc)
			}
		}
		return out
	case 2: // 时间范围
		var out []core.ArchiveSlot
		for _, arc := range core.CollectAllArchives(engine) {
			if arc.CreatedAt >= start && arc.CreatedAt <= end {
				out = append(out, arc)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
		return out
	case 3: // 按 id
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
