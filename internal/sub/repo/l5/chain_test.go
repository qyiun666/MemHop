// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package l5

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

func newTestEngine(t *testing.T) *storage.StorageEngine {
	t.Helper()
	engine, err := storage.Create(filepath.Join(t.TempDir(), "l5.meh"), 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close(&storage.IndexSnapshotData{}) })
	return engine
}

// mustWriteStep 直接写一条 ActionStep（l5 包不做 step 管理，测试用 record 原语造数）。
func mustWriteStep(t *testing.T, engine *storage.StorageEngine, chainID uint64, order uint16) uint64 {
	t.Helper()
	stepID := hash.HashID(fmt.Sprintf("%d:%d", chainID, order))
	step := &model.ActionStep{
		IDHash:    stepID,
		ChainID:   chainID,
		StepOrder: order,
		Action:    "do something",
	}
	if err := record.WriteActionStep(engine, stepID, step); err != nil {
		t.Fatal(err)
	}
	return stepID
}

func TestCreateAndGetChain(t *testing.T) {
	engine := newTestEngine(t)
	chainID, err := CreateChain(engine, "部署流程", "当用户说部署")
	if err != nil {
		t.Fatal(err)
	}
	if chainID != hash.HashID("部署流程:当用户说部署") {
		t.Errorf("chain id mismatch: %d", chainID)
	}
	got, err := GetChain(engine, hash.FormatHash(chainID))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "部署流程" || got.Trigger != "当用户说部署" {
		t.Errorf("chain mismatch: %+v", got)
	}
	if _, err := GetChain(engine, hash.FormatHash(999)); err == nil {
		t.Error("expected error for missing chain")
	}
}

func TestUpdateChain(t *testing.T) {
	engine := newTestEngine(t)
	chainID, err := CreateChain(engine, "t1", "tr1")
	if err != nil {
		t.Fatal(err)
	}
	chain, err := GetChain(engine, hash.FormatHash(chainID))
	if err != nil {
		t.Fatal(err)
	}
	chain.Status = model.ChainActive
	chain.Confidence = 0.9
	if err := UpdateChain(engine, hash.FormatHash(chainID), chain); err != nil {
		t.Fatal(err)
	}
	got, _ := GetChain(engine, hash.FormatHash(chainID))
	if got.Status != model.ChainActive || got.Confidence != 0.9 {
		t.Errorf("chain not updated: %+v", got)
	}
}

func TestDeleteChainCascadesSteps(t *testing.T) {
	engine := newTestEngine(t)
	chainID, _ := CreateChain(engine, "t1", "tr1")
	step1 := mustWriteStep(t, engine, chainID, 0)
	step2 := mustWriteStep(t, engine, chainID, 1)
	otherChain, _ := CreateChain(engine, "t2", "tr2")
	otherStep := mustWriteStep(t, engine, otherChain, 0)

	if !DeleteChain(engine, hash.FormatHash(chainID)) {
		t.Fatal("DeleteChain returned false")
	}
	if engine.Contains(chainID) || engine.Contains(step1) || engine.Contains(step2) {
		t.Error("chain and its steps should be deleted")
	}
	if !engine.Contains(otherChain) || !engine.Contains(otherStep) {
		t.Error("other chain should survive")
	}
	if _, err := GetChain(engine, hash.FormatHash(chainID)); err == nil {
		t.Error("deleted chain should not be readable")
	}
}
