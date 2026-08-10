// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package sub

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// writeArchive 写入 L4 归档记录。
func writeArchive(t *testing.T, engine *core.StorageEngine, arc *core.ArchiveSlot) {
	t.Helper()
	if err := core.WriteArchiveSlot(engine, arc.IDHash, arc); err != nil {
		t.Fatalf("write archive: %v", err)
	}
}

// TestGetArchive 按 ID 读取单条归档；不存在返回 ErrNotFound。
func TestGetArchive(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	topicHash := common.HashID("topic1")
	a1 := core.ArchiveSlot{IDHash: common.HashID("m1"), ContextID: topicHash, Content: "hello", CreatedAt: 1000, Role: 0, ContentType: core.ContentText}
	writeArchive(t, engine, &a1)

	got, err := db.GetArchive(common.FormatHash(a1.IDHash))
	if err != nil {
		t.Fatalf("GetArchive: %v", err)
	}
	if got.Content != "hello" || got.ContextID != topicHash {
		t.Fatalf("unexpected archive: %+v", got)
	}

	if _, err := db.GetArchive(common.FormatHash(12345)); err == nil {
		t.Fatal("want error for missing archive")
	}
}

// TestSearchL4TopicFilter 三模式叠加 TopicID 过滤。
func TestSearchL4TopicFilter(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	t1, t2 := common.HashID("t1"), common.HashID("t2")
	a1 := core.ArchiveSlot{IDHash: common.HashID("m1"), ContextID: t1, Content: "rust 所有权", CreatedAt: 1000, Role: 0, ContentType: core.ContentText}
	a2 := core.ArchiveSlot{IDHash: common.HashID("m2"), ContextID: t1, Content: "生命周期", CreatedAt: 2000, Role: 1, ContentType: core.ContentText}
	a3 := core.ArchiveSlot{IDHash: common.HashID("m3"), ContextID: t2, Content: "rust 生态", CreatedAt: 3000, Role: 0, ContentType: core.ContentText}
	writeArchive(t, engine, &a1)
	writeArchive(t, engine, &a2)
	writeArchive(t, engine, &a3)
	t1Hex, t2Hex := common.FormatHash(t1), common.FormatHash(t2)

	// Keyword + TopicID：a1 命中（a3 属于 t2 被排除）。
	out, err := db.SearchL4(L4Query{Keyword: "rust", TopicID: &t1Hex})
	if err != nil {
		t.Fatalf("SearchL4 keyword+topic: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != a1.IDHash {
		t.Fatalf("keyword+topic: want [m1], got %v", out)
	}

	// 时间范围 + TopicID：a1 only（Start 需 > 0，0 视为未设置）。
	out, err = db.SearchL4(L4Query{Start: 500, End: 1500, TopicID: &t1Hex})
	if err != nil {
		t.Fatalf("SearchL4 range+topic: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != a1.IDHash {
		t.Fatalf("range+topic: want [m1], got %v", out)
	}

	// IDs 模式 + TopicID：仅 a3。
	out, err = db.SearchL4(L4Query{IDs: []string{common.FormatHash(a1.IDHash), common.FormatHash(a2.IDHash), common.FormatHash(a3.IDHash)}, TopicID: &t2Hex})
	if err != nil {
		t.Fatalf("SearchL4 ids+topic: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != a3.IDHash {
		t.Fatalf("ids+topic: want [m3], got %v", out)
	}

	// 无效 TopicID 报错。
	if _, err := db.SearchL4(L4Query{Keyword: "rust", TopicID: strPtr("nothex")}); err == nil {
		t.Fatal("want error for invalid topic id")
	}
}

func strPtr(s string) *string { return &s }
