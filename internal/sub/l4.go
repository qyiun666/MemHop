// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 archive search operations of the sub layer.

package sub

import (
	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// L4Query 对话原文查询条件。三模式互斥，优先级：Keyword > 时间范围 > IDs；
// TopicID 在所有模式下叠加过滤。
type L4Query struct {
	Keyword string   `json:"keyword,omitempty"` // 模式1：内容子串匹配
	Start   int64    `json:"start,omitempty"`   // 模式2：时间范围 [Start, End]（毫秒）
	End     int64    `json:"end,omitempty"`
	IDs     []string `json:"ids,omitempty"`      // 模式3：按 id 读取
	TopicID *string  `json:"topic_id,omitempty"` // 叠加：仅返回该话题的归档
}

// SearchL4 搜索对话原文；条件全空时返回空结果。
func (db *DB) SearchL4(q L4Query) ([]core.ArchiveSlot, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	var out []core.ArchiveSlot
	switch {
	case q.Keyword != "":
		out = repo.QueryArchiveL4(db.engine, 1, q.Keyword, 0, 0, nil)
	case q.Start > 0 && q.End > 0:
		out = repo.QueryArchiveL4(db.engine, 2, "", q.Start, q.End, nil)
	case len(q.IDs) > 0:
		out = repo.QueryArchiveL4(db.engine, 3, "", 0, 0, q.IDs)
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

// GetArchive 按 ID 读取单条归档；不存在返回 ErrNotFound。
func (db *DB) GetArchive(id string) (*core.ArchiveSlot, error) {
	if err := db.beginRead(); err != nil {
		return nil, err
	}
	defer db.mu.RUnlock()
	out := repo.QueryArchiveL4(db.engine, 3, "", 0, 0, []string{id})
	if len(out) == 0 {
		return nil, common.NewError(common.ErrNotFound, "archive not found")
	}
	return &out[0], nil
}
