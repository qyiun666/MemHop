// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// A scene is a host session: its id comes from the host, and creating it
// twice must not rename or duplicate it.
func TestCreateSceneL2WithIDIsIdempotent(t *testing.T) {
	engine := tempEngine(t)
	if err := CreateSceneL2WithID(engine, core.DefaultAgentID, 4242, "session one"); err != nil {
		t.Fatalf("create scene: %v", err)
	}
	if err := CreateSceneL2WithID(engine, core.DefaultAgentID, 4242, "ignored"); err != nil {
		t.Fatalf("re-create same scene must be a no-op: %v", err)
	}
	slot, err := core.ReadSceneSlot(engine, core.DefaultAgentID, 4242)
	if err != nil {
		t.Fatalf("read scene: %v", err)
	}
	if slot.SceneName != "session one" {
		t.Fatalf("existing scene was renamed to %q", slot.SceneName)
	}
}

// One turn is one topic: both timestamps and the single keyword track land on
// the record, with the ID derived from the namespaced "turn:" key.
func TestCreateTurnTopicL2WritesSingleTrack(t *testing.T) {
	engine := tempEngine(t)
	const sceneID = uint64(7)
	topicID := core.ComputeTurnTopicID(sceneID, 1000, 1001)
	if !CreateTurnTopicL2(engine, core.DefaultAgentID, sceneID, topicID,
		[]string{"登录", "JWT"}, 1000, 1001) {
		t.Fatal("create turn topic")
	}
	got, err := core.ReadTopicSlot(engine, core.DefaultAgentID, topicID)
	if err != nil {
		t.Fatalf("read topic: %v", err)
	}
	if got.Depth != 1 || got.SceneID != sceneID {
		t.Fatalf("unexpected placement: %+v", got)
	}
	if !slices.Equal(got.FusedKeywords, []string{"登录", "JWT"}) {
		t.Fatalf("keyword track mismatch: %v", got.FusedKeywords)
	}
	if got.UserTimestamp != 1000 || got.AgentTimestamp != 1001 {
		t.Fatalf("timestamp mismatch: %+v", got)
	}
}

// TestListTopicsL2FromL2Meta verifies listing consumes the L2MetaIndex cache
// with identical semantics to the record scan: depth filtering (mode 1),
// scene filtering (mode 2), UserTimestamp ascending sort, single-topic
// lookup (mode 3) and full field fidelity.
func TestListTopicsL2FromL2Meta(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "list.meh"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close(nil) })

	sceneA := core.NewSceneSlot(1, "a").SceneID
	sceneB := core.NewSceneSlot(2, "b").SceneID
	parentID := uint64(12)
	// Timestamps written out of order on purpose; depth 3 must be filtered
	// out by depth<=2 modes; full field set checks cache-vs-record fidelity.
	raw := []core.TopicSlot{
		{ID: 11, SceneID: sceneA, Depth: 1, FusedKeywords: []string{"k1"},
			UserTimestamp: 300, L4Refs: []uint64{601}},
		{ID: 12, SceneID: sceneB, Depth: 1, FusedKeywords: []string{"k2", "a2"},
			AgentTimestamp: 400, UserTimestamp: 100, ChildrenIDs: []uint64{13}},
		{ID: 13, SceneID: sceneA, Depth: 2, FusedKeywords: []string{"f3"},
			UserTimestamp: 200, ParentID: &parentID},
		{ID: 14, SceneID: sceneB, Depth: 3, FusedKeywords: []string{"k4"},
			UserTimestamp: 150},
	}
	for i := range raw {
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, raw[i].ID, &raw[i]); err != nil {
			t.Fatalf("write topic %d: %v", raw[i].ID, err)
		}
	}

	l2Meta := index.BuildL2MetaFromEngine(engine, core.DefaultAgentID)
	if l2Meta.Len() != len(raw) {
		t.Fatalf("L2MetaIndex entries = %d, want %d", l2Meta.Len(), len(raw))
	}

	q := func(mode uint8, sceneID uint64, depth uint8) ([]core.TopicSlot, error) {
		return ListTopicsL2(TopicListQuery{
			Engine:  engine,
			MetaIdx: l2Meta,
			SceneID: sceneID,
			Depth:   depth,
			Num:     mode,
		})
	}

	t.Run("mode1_filters_depth_and_sorts_asc", func(t *testing.T) {
		got, err := q(1, 0, 2)
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
		got, err := q(2, sceneA, 2)
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
		got, err := q(3, 11, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].ID != 11 {
			t.Fatalf("mode 3 = %+v, want single topic 11", got)
		}
	})

	t.Run("fields_match_record_exactly", func(t *testing.T) {
		got, err := q(1, 0, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, tp := range got {
			record, err := core.ReadTopicSlot(engine, core.DefaultAgentID, tp.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tp, *record) {
				t.Errorf("topic %d rebuilt from cache differs from record:\ncache:  %+v\nrecord: %+v",
					tp.ID, tp, *record)
			}
		}
	})

	t.Run("nil_meta_falls_back_to_scan", func(t *testing.T) {
		gotCache, err := q(1, 0, 2)
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
			FusedKeywords: []string{"k5"}, UserTimestamp: 50}
		l2Meta.Update(index.L2MetaFromTopic(&tp))
		got, err := q(1, 0, 2)
		if err != nil {
			t.Fatal(err)
		}
		// mode 1 depth<=2 sees 3 of the 4 raw topics; +1 after Update.
		if len(got) != 4 || got[0].ID != newID {
			t.Errorf("after Update: got %d topics, first=%d; want 4 topics, first=%d",
				len(got), got[0].ID, newID)
		}
		l2Meta.Remove(newID)
		got, err = q(1, 0, 2)
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
	const sceneID = uint64(4242)
	if err := CreateSceneL2WithID(engine, core.DefaultAgentID, sceneID, "scene-usage-1"); err != nil {
		t.Fatalf("create scene: %v", err)
	}
	if _, err := TouchSceneUsage(engine, core.DefaultAgentID, sceneID, 1000); err != nil {
		t.Fatalf("first touch: %v", err)
	}
	touched, err := TouchSceneUsage(engine, core.DefaultAgentID, sceneID, 2000)
	if err != nil {
		t.Fatalf("second touch: %v", err)
	}
	if touched.HitCount != 2 || touched.LastHitAt != 2000 {
		t.Fatalf("returned record not the bumped one: %+v", touched)
	}
	slot, err := core.ReadSceneSlot(engine, core.DefaultAgentID, sceneID)
	if err != nil {
		t.Fatalf("read scene: %v", err)
	}
	if slot.HitCount != 2 || slot.LastHitAt != 2000 {
		t.Fatalf("usage mismatch: %+v", slot)
	}
}

func TestOverwriteSceneL3IDCorrection(t *testing.T) {
	engine := tempEngine(t)
	const sceneID = uint64(99)
	if err := CreateSceneL2WithID(engine, core.DefaultAgentID, sceneID, "scene-l3"); err != nil {
		t.Fatalf("create scene: %v", err)
	}

	// First anchor is write-once: a second normal Set cannot steal it.
	if err := SetSceneL3ID(engine, core.DefaultAgentID, sceneID, 100); err != nil {
		t.Fatalf("first anchor: %v", err)
	}
	if err := SetSceneL3ID(engine, core.DefaultAgentID, sceneID, 200); err != nil {
		t.Fatalf("second set: %v", err)
	}
	if slot, _ := core.ReadSceneSlot(engine, core.DefaultAgentID, sceneID); slot.L3ID != 100 {
		t.Fatalf("write-once must keep 100, got %d", slot.L3ID)
	}
	// Overwrite corrects the anchor.
	if err := OverwriteSceneL3ID(engine, core.DefaultAgentID, sceneID, 200); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if slot, _ := core.ReadSceneSlot(engine, core.DefaultAgentID, sceneID); slot.L3ID != 200 {
		t.Fatalf("overwrite must move to 200, got %d", slot.L3ID)
	}
	// Overwrite with 0 clears the anchor.
	if err := OverwriteSceneL3ID(engine, core.DefaultAgentID, sceneID, 0); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if slot, _ := core.ReadSceneSlot(engine, core.DefaultAgentID, sceneID); slot.L3ID != 0 {
		t.Fatalf("clear must reset to 0, got %d", slot.L3ID)
	}
}
