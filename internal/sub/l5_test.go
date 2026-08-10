// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package sub

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// writeChain 写入 L5 动作链记录。
func writeChain(t *testing.T, engine *core.StorageEngine, c *core.ActionChainSlot) {
	t.Helper()
	if err := core.WriteActionChainSlot(engine, c.IDHash, c); err != nil {
		t.Fatalf("write chain: %v", err)
	}
}

// TestListL5 全量、过滤与排序。
func TestListL5(t *testing.T) {
	engine := newTestEngine(t)
	db := &DB{engine: engine}
	c1 := core.ActionChainSlot{IDHash: common.HashID("c1"), Title: "修复编译错误", Status: core.ChainActive, TriggerCount: 5, UpdatedAt: 3000}
	c2 := core.ActionChainSlot{IDHash: common.HashID("c2"), Title: "代码审查流程", Status: core.ChainDraft, TriggerCount: 1, UpdatedAt: 1000}
	c3 := core.ActionChainSlot{IDHash: common.HashID("c3"), Title: "发布版本", Status: core.ChainActive, TriggerCount: 2, UpdatedAt: 2000}
	writeChain(t, engine, &c1)
	writeChain(t, engine, &c2)
	writeChain(t, engine, &c3)

	// 全量：按 UpdatedAt 降序 → c1, c3, c2。
	out, err := db.ListL5(L5ListQuery{})
	if err != nil {
		t.Fatalf("ListL5: %v", err)
	}
	if len(out) != 3 || out[0].IDHash != c1.IDHash || out[1].IDHash != c3.IDHash || out[2].IDHash != c2.IDHash {
		t.Fatalf("all: want [c1 c3 c2], got %v", idsOfChains(out))
	}

	// Status 过滤。
	out, err = db.ListL5(L5ListQuery{Status: strPtr("active")})
	if err != nil {
		t.Fatalf("ListL5 status: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("status active: want 2 chains, got %d", len(out))
	}

	// MinTriggerCount 过滤。
	min := uint32(2)
	out, err = db.ListL5(L5ListQuery{MinTriggerCount: &min})
	if err != nil {
		t.Fatalf("ListL5 min trigger: %v", err)
	}
	if len(out) != 2 || out[0].IDHash != c1.IDHash {
		t.Fatalf("min trigger: want [c1 c3], got %v", idsOfChains(out))
	}

	// Keyword 大小写不敏感子串匹配。
	out, err = db.ListL5(L5ListQuery{Keyword: "编译"})
	if err != nil {
		t.Fatalf("ListL5 keyword: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != c1.IDHash {
		t.Fatalf("keyword: want [c1], got %v", idsOfChains(out))
	}

	// 组合过滤。
	out, err = db.ListL5(L5ListQuery{Status: strPtr("active"), Keyword: "发布"})
	if err != nil {
		t.Fatalf("ListL5 combo: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != c3.IDHash {
		t.Fatalf("combo: want [c3], got %v", idsOfChains(out))
	}
}

// TestListL5Empty 空库返回空切片。
func TestListL5Empty(t *testing.T) {
	db := &DB{engine: newTestEngine(t)}
	out, err := db.ListL5(L5ListQuery{})
	if err != nil {
		t.Fatalf("ListL5: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("want 0 chains, got %d", len(out))
	}
}

func idsOfChains(chains []core.ActionChainSlot) []uint64 {
	out := make([]uint64, len(chains))
	for i, c := range chains {
		out[i] = c.IDHash
	}
	return out
}
