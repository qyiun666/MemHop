// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

func TestCreateTopicL2WithIDSameTimestampDifferentText(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "topics.meh"), 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close(&core.IndexSnapshotData{}) })

	sceneID := core.NewSceneSlot("scene").SceneID
	id1 := core.ComputeTopicIDForText(sceneID, 1000, "hello")
	id2 := core.ComputeTopicIDForText(sceneID, 1000, "world")
	if id1 == id2 {
		t.Fatal("different text must produce different topic IDs")
	}
	if !CreateTopicL2WithID(engine, sceneID, id1, []string{"hello"}, 1000, 0) {
		t.Fatal("create first topic")
	}
	if !CreateTopicL2WithID(engine, sceneID, id2, []string{"world"}, 1000, 0) {
		t.Fatal("create second topic")
	}
	if _, err := core.ReadTopicSlot(engine, id1); err != nil {
		t.Fatalf("read first topic: %v", err)
	}
	if _, err := core.ReadTopicSlot(engine, id2); err != nil {
		t.Fatalf("read second topic: %v", err)
	}
}

// TestListTopicsL2FromL2Meta verifies candidate generation consumes the
// L2MetaIndex cache with identical semantics to the record scan: depth
// filtering (mode 1), scene filtering (mode 2), UserTimestamp ascending
// sort, single-topic lookup (mode 3) and full field fidelity.
func TestListTopicsL2FromL2Meta(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "list.meh"), 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close(&core.IndexSnapshotData{}) })

	sceneA := core.NewSceneSlot("a").SceneID
	sceneB := core.NewSceneSlot("b").SceneID
	parentID := uint64(12)
	// Timestamps written out of order on purpose; depth 3 must be filtered
	// out by depth<=2 modes; full field set checks cache-vs-record fidelity.
	raw := []core.TopicSlot{
		{ID: 11, SceneID: sceneA, Depth: 1, UserKeywords: []string{"k1"},
			UserTimestamp: 300, L3Refs: []uint64{501}, L4Refs: []uint64{601},
			CentroidPageRef: 701},
		{ID: 12, SceneID: sceneB, Depth: 1, UserKeywords: []string{"k2"},
			AgentKeywords: []string{"a2"}, AgentTimestamp: 400, UserTimestamp: 100,
			ChildrenIDs: []uint64{13}},
		{ID: 13, SceneID: sceneA, Depth: 2, FusedKeywords: []string{"f3"},
			UserTimestamp: 200, ParentID: &parentID},
		{ID: 14, SceneID: sceneB, Depth: 3, UserKeywords: []string{"k4"},
			UserTimestamp: 150},
	}
	for i := range raw {
		if err := core.WriteTopicSlot(engine, raw[i].ID, &raw[i]); err != nil {
			t.Fatalf("write topic %d: %v", raw[i].ID, err)
		}
	}

	l2Meta := index.BuildL2MetaFromEngine(engine)
	if l2Meta.Len() != len(raw) {
		t.Fatalf("L2MetaIndex entries = %d, want %d", l2Meta.Len(), len(raw))
	}

	q := func(mode uint8, sceneID string, depth uint8) ([]core.TopicSlot, error) {
		return ListTopicsL2(TopicListQuery{
			Engine:  engine,
			MetaIdx: l2Meta,
			SceneID: sceneID,
			Depth:   depth,
			Num:     mode,
		})
	}

	t.Run("mode1_filters_depth_and_sorts_asc", func(t *testing.T) {
		got, err := q(1, "", 2)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs := []uint64{12, 13, 11} // UserTimestamp 100, 200, 300
		if len(got) != len(wantIDs) {
			t.Fatalf("got %d topics, want %d", len(got), len(wantIDs))
		}
		for i, id := range wantIDs {
			if got[i].ID != id {
				t.Errorf("sorted[%d].ID = %d, want %d", i, got[i].ID, id)
			}
		}
		for i := 1; i < len(got); i++ {
			if got[i].UserTimestamp < got[i-1].UserTimestamp {
				t.Errorf("not sorted by UserTimestamp: %d after %d",
					got[i].UserTimestamp, got[i-1].UserTimestamp)
			}
		}
		for _, tp := range got {
			if tp.Depth > 2 {
				t.Errorf("depth-3 topic %d leaked into mode 1", tp.ID)
			}
		}
	})

	t.Run("mode2_filters_by_scene", func(t *testing.T) {
		got, err := q(2, common.FormatHash(sceneA), 2)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs := []uint64{13, 11} // sceneA only, asc by timestamp
		if len(got) != len(wantIDs) {
			t.Fatalf("got %d topics, want %d", len(got), len(wantIDs))
		}
		for i, id := range wantIDs {
			if got[i].ID != id {
				t.Errorf("scene-filtered[%d].ID = %d, want %d", i, got[i].ID, id)
			}
		}
	})

	t.Run("mode3_reads_single_topic", func(t *testing.T) {
		got, err := q(3, common.FormatHash(11), 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].ID != 11 {
			t.Fatalf("mode 3 = %+v, want single topic 11", got)
		}
	})

	t.Run("fields_match_record_exactly", func(t *testing.T) {
		got, err := q(1, "", 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, tp := range got {
			record, err := core.ReadTopicSlot(engine, tp.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tp, record[0]) {
				t.Errorf("topic %d rebuilt from cache differs from record:\ncache:  %+v\nrecord: %+v",
					tp.ID, tp, record[0])
			}
		}
	})

	t.Run("nil_meta_falls_back_to_scan", func(t *testing.T) {
		gotCache, err := q(1, "", 2)
		if err != nil {
			t.Fatal(err)
		}
		gotScan, err := ListTopicsL2(TopicListQuery{Engine: engine, Depth: 2, Num: 1})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotCache, gotScan) {
			t.Errorf("cache path differs from scan path:\ncache: %+v\nscan:  %+v", gotCache, gotScan)
		}
	})

	t.Run("incremental_updates_reflect_in_listing", func(t *testing.T) {
		// Simulate write-path sync: new topic inserted via Update, then
		// removed; listing must follow both.
		newID := uint64(15)
		tp := core.TopicSlot{ID: newID, SceneID: sceneB, Depth: 1,
			UserKeywords: []string{"k5"}, UserTimestamp: 50}
		l2Meta.Update(index.L2MetaFromTopic(&tp))
		got, err := q(1, "", 2)
		if err != nil {
			t.Fatal(err)
		}
		// mode 1 depth<=2 sees 3 of the 4 raw topics; +1 after Update.
		if len(got) != 4 || got[0].ID != newID {
			t.Errorf("after Update: got %d topics, first=%d; want 4 topics, first=%d",
				len(got), got[0].ID, newID)
		}
		l2Meta.Remove(newID)
		got, err = q(1, "", 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Errorf("after Remove: got %d topics, want 3", len(got))
		}
	})
}

func TestTouchSceneUsageIncrements(t *testing.T) {
	engine := tempEngine(t)
	sceneID := core.NewSceneSlot("scene-usage-1").SceneID
	if _, err := CreateSceneL2(engine, "scene-usage-1"); err != nil {
		t.Fatalf("create scene: %v", err)
	}
	if err := TouchSceneUsage(engine, sceneID, 1000); err != nil {
		t.Fatalf("first touch: %v", err)
	}
	if err := TouchSceneUsage(engine, sceneID, 2000); err != nil {
		t.Fatalf("second touch: %v", err)
	}
	slot, err := core.ReadSceneSlot(engine, sceneID)
	if err != nil {
		t.Fatalf("read scene: %v", err)
	}
	if slot.HitCount != 2 || slot.LastHitAt != 2000 {
		t.Fatalf("usage mismatch: %+v", slot)
	}
}
