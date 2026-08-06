// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package l4

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

func newTestEngine(t *testing.T) *storage.StorageEngine {
	t.Helper()
	engine, err := storage.Create(filepath.Join(t.TempDir(), "l4.meh"), 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close(&storage.IndexSnapshotData{}) })
	return engine
}

func TestAppendAndQueryByID(t *testing.T) {
	engine := newTestEngine(t)
	ctx := hash.FormatHash(hash.HashID("scene-a"))
	id1, err := AppendArchive(engine, ctx, 0, model.ContentText, "hello world", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if id1 == 0 {
		t.Error("archive id should not be zero")
	}
	id2, err := AppendArchive(engine, ctx, 1, model.ContentText, "second message", 2000)
	if err != nil {
		t.Fatal(err)
	}
	got := QueryArchive(engine, 3, "", 0, 0, []string{hash.FormatHash(id1), hash.FormatHash(id2), hash.FormatHash(999)})
	if len(got) != 2 {
		t.Fatalf("expected 2 by id, got %d", len(got))
	}
	if got[0].Content != "hello world" || got[0].Role != 0 {
		t.Errorf("archive mismatch: %+v", got[0])
	}
}

func TestQueryByKeyword(t *testing.T) {
	engine := newTestEngine(t)
	ctx := hash.FormatHash(hash.HashID("scene-a"))
	AppendArchive(engine, ctx, 0, model.ContentText, "部署 memhop 到生产环境", 1000)
	AppendArchive(engine, ctx, 1, model.ContentText, "好的，先检查配置", 2000)
	AppendArchive(engine, ctx, 0, model.ContentText, "明天继续", 3000)

	got := QueryArchive(engine, 1, "memhop", 0, 0, nil)
	if len(got) != 1 || got[0].Content != "部署 memhop 到生产环境" {
		t.Errorf("keyword query mismatch: %+v", got)
	}
	if got := QueryArchive(engine, 1, "不存在词", 0, 0, nil); len(got) != 0 {
		t.Errorf("unexpected hits: %+v", got)
	}
}

func TestQueryByTimeRange(t *testing.T) {
	engine := newTestEngine(t)
	ctx := hash.FormatHash(hash.HashID("scene-a"))
	AppendArchive(engine, ctx, 0, model.ContentText, "m1", 1000)
	AppendArchive(engine, ctx, 1, model.ContentText, "m2", 2000)
	AppendArchive(engine, ctx, 0, model.ContentText, "m3", 3000)

	got := QueryArchive(engine, 2, "", 1500, 3000, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 in range, got %d", len(got))
	}
	// 按 CreatedAt 升序
	if got[0].CreatedAt != 2000 || got[1].CreatedAt != 3000 {
		t.Errorf("not sorted: %+v", got)
	}
}

func TestQueryInvalidNum(t *testing.T) {
	engine := newTestEngine(t)
	if got := QueryArchive(engine, 9, "", 0, 0, nil); got != nil {
		t.Errorf("invalid num should return nil, got %+v", got)
	}
}
