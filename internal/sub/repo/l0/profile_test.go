// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package l0

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

func newTestEngine(t *testing.T) *storage.StorageEngine {
	t.Helper()
	engine, err := storage.Create(filepath.Join(t.TempDir(), "l0.meh"), 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close(&storage.IndexSnapshotData{}) })
	return engine
}

func TestGetProfileNotFound(t *testing.T) {
	engine := newTestEngine(t)
	if _, err := GetProfile(engine); err == nil {
		t.Fatal("expected ErrNotFound for missing profile")
	}
}

func TestUpdateProfileRoundtrip(t *testing.T) {
	engine := newTestEngine(t)
	slot := &model.ProfileSlot{
		Name:        "memhop-agent",
		Role:        "assistant",
		Personality: "helpful",
		Preferences: map[string]string{"lang": "zh"},
		Lexicon:     map[string]string{"mem": "memory"},
		StyleTraits: []string{"concise"},
	}
	if err := UpdateProfile(engine, slot); err != nil {
		t.Fatal(err)
	}
	got, err := GetProfile(engine)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "memhop-agent" || got.Personality != "helpful" {
		t.Errorf("profile mismatch: %+v", got)
	}
	// 覆盖更新
	got.Personality = "taciturn"
	if err := UpdateProfile(engine, got); err != nil {
		t.Fatal(err)
	}
	got2, err := GetProfile(engine)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Personality != "taciturn" {
		t.Errorf("profile not updated: %+v", got2)
	}
}
