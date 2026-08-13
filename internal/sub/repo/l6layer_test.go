// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

func tempEngine(t *testing.T) *core.StorageEngine {
	t.Helper()
	eng, err := core.Create(filepath.Join(t.TempDir(), "test.meh"), 128)
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { eng.Close(&core.IndexSnapshotData{}) })
	return eng
}

func TestUpsertSceneUsageIncrements(t *testing.T) {
	engine := tempEngine(t)
	if err := UpsertSceneUsage(engine, 42, 1000); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := UpsertSceneUsage(engine, 42, 2000); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	all := CollectAllSceneUsages(engine)
	if len(all) != 1 {
		t.Fatalf("want 1 usage record, got %d", len(all))
	}
	u := all[0]
	if u.SceneID != 42 || u.HitCount != 2 || u.LastHitAt != 2000 {
		t.Fatalf("usage mismatch: %+v", u)
	}
}

func TestCollectAllSceneUsagesMultipleScenes(t *testing.T) {
	engine := tempEngine(t)
	for _, sid := range []uint64{1, 2, 3} {
		if err := UpsertSceneUsage(engine, sid, 1000); err != nil {
			t.Fatalf("upsert scene %d: %v", sid, err)
		}
	}
	all := CollectAllSceneUsages(engine)
	if len(all) != 3 {
		t.Fatalf("want 3 usage records, got %d", len(all))
	}
}
