// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// countL2Meta counts the cached topics by iterating, the way every reader does.
func countL2Meta(idx *index.L2MetaIndex) int {
	n := 0
	for range idx.Iter() {
		n++
	}
	return n
}

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
	topicID := core.ComputeTurnTopicID(sceneID, 1001)
	if err := CreateTurnTopicL2(engine, core.DefaultAgentID, sceneID, topicID,
		[]string{"登录", "JWT"}, 1000, 1001); err != nil {
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

// TestListTopicsL2FromL2Meta verifies the listing reads the L2MetaIndex mirror:
// depth filtering, scene filtering when asked for one scene, UserTimestamp
// ascending sort, and a mirror entry that rebuilds to the stored record exactly.
func TestListTopicsL2FromL2Meta(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "list.meh"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })

	sceneA := core.NewSceneSlot(1, "a").SceneID
	sceneB := core.NewSceneSlot(2, "b").SceneID
	parentID := uint64(12)
	// Timestamps written out of order on purpose; depth 3 must be filtered
	// out of a depth<=2 listing; full field set checks cache-vs-record fidelity.
	raw := []core.TopicSlot{
		{ID: 11, SceneID: sceneA, Depth: 1, FusedKeywords: []string{"k1"},
			UserTimestamp: 300},
		{ID: 12, SceneID: sceneB, Depth: 1, FusedKeywords: []string{"k2", "a2"},
			AgentTimestamp: 400, UserTimestamp: 100},
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
	if countL2Meta(l2Meta) != len(raw) {
		t.Fatalf("L2MetaIndex entries = %d, want %d", countL2Meta(l2Meta), len(raw))
	}

	q := func(byScene bool, sceneID uint64, depth uint8) []core.TopicSlot {
		return ListTopicsL2(TopicListQuery{
			MetaIdx: l2Meta,
			SceneID: sceneID,
			Depth:   depth,
			ByScene: byScene,
		})
	}

	t.Run("domain_wide_filters_depth_and_sorts_asc", func(t *testing.T) {
		got := q(false, 0, 2)
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
				t.Errorf("depth-3 topic %d leaked into the domain-wide listing", tp.ID)
			}
		}
	})

	t.Run("by_scene_filters", func(t *testing.T) {
		got := q(true, sceneA, 2)
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

	t.Run("fields_match_record_exactly", func(t *testing.T) {
		got := q(false, 0, 2)
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

	t.Run("incremental_updates_reflect_in_listing", func(t *testing.T) {
		// Simulate write-path sync: new topic inserted via Update, then
		// removed; listing must follow both.
		newID := uint64(15)
		tp := core.TopicSlot{ID: newID, SceneID: sceneB, Depth: 1,
			FusedKeywords: []string{"k5"}, UserTimestamp: 50}
		l2Meta.Update(index.L2MetaFromTopic(&tp))
		got := q(false, 0, 2)
		// A domain-wide depth<=2 listing sees 3 of the 4 raw topics; +1 after Update.
		if len(got) != 4 || got[0].ID != newID {
			t.Errorf("after Update: got %d topics, first=%d; want 4 topics, first=%d",
				len(got), got[0].ID, newID)
		}
		l2Meta.Remove(newID)
		got = q(false, 0, 2)
		if len(got) != 3 {
			t.Errorf("after Remove: got %d topics, want 3", len(got))
		}
	})
}

// One read of a scene opens one turn: the turn counter moves and the caller
// gets the bumped record back.
func TestOpenSceneTurnAdvancesTurnSeq(t *testing.T) {
	engine := tempEngine(t)
	const sceneID = uint64(4242)
	if err := CreateSceneL2WithID(engine, core.DefaultAgentID, sceneID, "scene-usage-1"); err != nil {
		t.Fatalf("create scene: %v", err)
	}
	if _, err := OpenSceneTurn(engine, core.DefaultAgentID, sceneID); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	opened, err := OpenSceneTurn(engine, core.DefaultAgentID, sceneID)
	if err != nil {
		t.Fatalf("second turn: %v", err)
	}
	if opened.TurnSeq != 2 {
		t.Fatalf("returned record not the bumped one: %+v", opened)
	}
	slot, err := core.ReadSceneSlot(engine, core.DefaultAgentID, sceneID)
	if err != nil {
		t.Fatalf("read scene: %v", err)
	}
	if slot.TurnSeq != 2 {
		t.Fatalf("stored turn counter: %+v", slot)
	}
}

// SetSceneL3ID is the routing primitive: it claims an unanchored scene and
// never moves one that already has a domain. Host corrections take the
// read-modify-write path in the composition root instead.
func TestSetSceneL3IDIsWriteOnce(t *testing.T) {
	engine := tempEngine(t)
	const sceneID = uint64(99)
	if err := CreateSceneL2WithID(engine, core.DefaultAgentID, sceneID, "scene-l3"); err != nil {
		t.Fatalf("create scene: %v", err)
	}
	if err := SetSceneL3ID(engine, core.DefaultAgentID, sceneID, 100); err != nil {
		t.Fatalf("first anchor: %v", err)
	}
	if err := SetSceneL3ID(engine, core.DefaultAgentID, sceneID, 200); err != nil {
		t.Fatalf("second set: %v", err)
	}
	if slot, _ := core.ReadSceneSlot(engine, core.DefaultAgentID, sceneID); slot.L3ID != 100 {
		t.Fatalf("write-once must keep 100, got %d", slot.L3ID)
	}
}

// A topic name is the host's, so writing it must not disturb anything the
// engine put on the record — and the cache path has to agree with the record
// path, or the same topic reads as named in one and unnamed in the other.
func TestRenameTopicL2KeepsTheRestOfTheRecord(t *testing.T) {
	engine := tempEngine(t)
	const sceneID = uint64(7)
	topicID := core.ComputeTurnTopicID(sceneID, 1)
	if err := CreateTurnTopicL2(engine, core.DefaultAgentID, sceneID, topicID, []string{"登录", "JWT"}, 1000, 1001); err != nil {
		t.Fatal("create turn topic")
	}
	const want = "决定把 L5 让给计划树的那一轮"
	got, err := RenameTopicL2(engine, core.DefaultAgentID, topicID, want)
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got.Name != want {
		t.Fatalf("name not written: %+v", got)
	}
	if !slices.Equal(got.FusedKeywords, []string{"登录", "JWT"}) {
		t.Fatalf("keyword track disturbed: %v", got.FusedKeywords)
	}
	if got.SceneID != sceneID || got.Depth != 1 {
		t.Fatalf("scene ownership or depth disturbed: %+v", got)
	}
	if cached := index.L2MetaFromTopic(got).ToTopicSlot(); cached.Name != want {
		t.Fatalf("cache path lost the name: %+v", cached)
	}
}

// A name addresses a turn that already settled: naming one that is not there
// reports ErrNotFound instead of inventing a topic behind an id nothing else
// refers to.
func TestRenameTopicL2MissingTopic(t *testing.T) {
	engine := tempEngine(t)
	const missing = uint64(424242)
	if _, err := RenameTopicL2(engine, core.DefaultAgentID, missing, "nobody"); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := core.ReadTopicSlot(engine, core.DefaultAgentID, missing); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("the refused rename must leave no record behind, got %v", err)
	}
}

// Settling a turn twice is the replay path, and only the engine-owned half is
// rewritten: the label the host gave that turn is not the engine's to erase, so
// re-distilling the keywords must leave it in place.
func TestCreateTurnTopicL2ReplayKeepsHostName(t *testing.T) {
	engine := tempEngine(t)
	const sceneID = uint64(7)
	topicID := core.ComputeTurnTopicID(sceneID, 1)
	if err := CreateTurnTopicL2(engine, core.DefaultAgentID, sceneID, topicID, []string{"登录"}, 1000, 1001); err != nil {
		t.Fatal("first settle")
	}
	const hostName = "把登录链路讲清楚的那一轮"
	if _, err := RenameTopicL2(engine, core.DefaultAgentID, topicID, hostName); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := CreateTurnTopicL2(engine, core.DefaultAgentID, sceneID, topicID, []string{"刷新", "token"}, 1000, 1100); err != nil {
		t.Fatal("replay settle")
	}
	got, err := core.ReadTopicSlot(engine, core.DefaultAgentID, topicID)
	if err != nil {
		t.Fatalf("read replayed topic: %v", err)
	}
	if got.Name != hostName {
		t.Fatalf("replay un-named the turn the host named: %+v", got)
	}
	if !slices.Equal(got.FusedKeywords, []string{"刷新", "token"}) {
		t.Fatalf("replay must rewrite the keyword track, got %v", got.FusedKeywords)
	}
}

// The same replay must not move the turn either. Once compression has sunk it
// under a fused group, that group's summary is what the scene shows in its place:
// a replay that reset the depth would bring the turn's own originals back to the
// surface beside the summary, and leave the group one child short.
func TestCreateTurnTopicL2ReplayKeepsSunkPosition(t *testing.T) {
	engine := tempEngine(t)
	const sceneID = uint64(7)
	const parentID = uint64(555)
	topicID := core.ComputeTurnTopicID(sceneID, 1)
	if err := CreateTurnTopicL2(engine, core.DefaultAgentID, sceneID, topicID, []string{"登录"}, 1000, 1001); err != nil {
		t.Fatal("first settle")
	}
	if err := CompressTopicsL2(engine, core.DefaultAgentID, []uint64{topicID}, parentID); err != nil {
		t.Fatalf("sink the turn: %v", err)
	}
	if err := CreateTurnTopicL2(engine, core.DefaultAgentID, sceneID, topicID, []string{"刷新", "token"}, 1000, 1100); err != nil {
		t.Fatal("replay settle")
	}
	got, err := core.ReadTopicSlot(engine, core.DefaultAgentID, topicID)
	if err != nil {
		t.Fatalf("read replayed topic: %v", err)
	}
	if got.Depth != 2 || got.ParentID == nil || *got.ParentID != parentID {
		t.Fatalf("replay returned a sunk turn to the surface: depth=%d parent=%v", got.Depth, got.ParentID)
	}
	if !slices.Equal(got.FusedKeywords, []string{"刷新", "token"}) {
		t.Fatalf("replay must still rewrite the keyword track, got %v", got.FusedKeywords)
	}
}

// A stored record that will not decode leaves the replay unable to say where that
// turn belongs, so the settle refuses and rewrites nothing: guessing depth 1 would
// put a second version of one turn on the read path. The read has to name that
// failure, or a caller cannot tell it apart from a transport error.
func TestCreateTurnTopicL2RefusesUndecodableRecord(t *testing.T) {
	engine := tempEngine(t)
	const sceneID = uint64(7)
	topicID := core.ComputeTurnTopicID(sceneID, 1)
	if err := CreateTurnTopicL2(engine, core.DefaultAgentID, sceneID, topicID, []string{"登录"}, 1000, 1001); err != nil {
		t.Fatal("first settle")
	}
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL2Topic, topicID,
		[]byte(`{"id":`)); err != nil {
		t.Fatalf("replace the payload with an undecodable one: %v", err)
	}
	if err := CreateTurnTopicL2(engine, core.DefaultAgentID, sceneID, topicID, []string{"刷新"}, 1000, 1100); common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("a replay must refuse to place a turn whose stored record will not read, and say why, got %v", err)
	}
	if _, err := core.ReadTopicLenient(engine, core.DefaultAgentID, topicID); common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("the read must report a decode failure by name, got %v", err)
	}
}

// Two topics at the same depth can share one user timestamp — a fused parent is
// stamped with its group's earliest turn's timestamp, so any same-depth topic
// holding that instant ties with it on both sort keys. Ties have to break on
// something the scan does not decide, or the same scene answers in one order on
// one read and another order on the next.
func TestListTopicsL2BreaksTiesOnID(t *testing.T) {
	engine := tempEngine(t)
	const sceneID = uint64(7)
	for _, id := range []uint64{30, 10, 20} {
		topic := core.TopicSlot{
			ID: id, SceneID: sceneID, Depth: 1, FusedKeywords: []string{"k"},
			UserTimestamp: 1000, AgentTimestamp: 2000,
		}
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, id, &topic); err != nil {
			t.Fatalf("write topic %d: %v", id, err)
		}
	}
	for attempt := range 3 {
		got := ListTopicsL2(TopicListQuery{
			MetaIdx: index.BuildL2MetaFromEngine(engine, core.DefaultAgentID),
			SceneID: sceneID, Depth: 1, ByScene: true,
		})
		if len(got) != 3 {
			t.Fatalf("attempt %d: want 3 topics, got %d", attempt, len(got))
		}
		for i, want := range []uint64{10, 20, 30} {
			if got[i].ID != want {
				t.Fatalf("attempt %d: position %d wants id %d, got %d", attempt, i, want, got[i].ID)
			}
		}
	}
}

// A group member the listing names but the payload will not decode is a read
// failure, not a member that went away. The parent summary is already on disk
// when this runs, so sinking the rest would leave the scene showing both the
// group's summary and that member's originals — the whole sink has to stop.
func TestCompressTopicsL2RefusesUnreadableMember(t *testing.T) {
	engine := tempEngine(t)
	const sceneID = uint64(7)
	readable := core.TopicSlot{ID: 21, SceneID: sceneID, Depth: 1,
		FusedKeywords: []string{"k1"}, UserTimestamp: 100}
	corrupt := core.TopicSlot{ID: 22, SceneID: sceneID, Depth: 1,
		FusedKeywords: []string{"k2"}, UserTimestamp: 200}
	for _, tp := range []core.TopicSlot{readable, corrupt} {
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, tp.ID, &tp); err != nil {
			t.Fatalf("write topic %d: %v", tp.ID, err)
		}
	}
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL2Topic, corrupt.ID,
		[]byte(`{"id":`)); err != nil {
		t.Fatalf("replace the payload with an undecodable one: %v", err)
	}

	err := CompressTopicsL2(engine, core.DefaultAgentID, []uint64{readable.ID, corrupt.ID}, 99)
	if common.CodeOf(err) != common.ErrIO {
		t.Fatalf("want ErrIO for an unreadable member, got %v", err)
	}
	got, rerr := core.ReadTopicSlot(engine, core.DefaultAgentID, readable.ID)
	if rerr != nil {
		t.Fatalf("read the untouched sibling: %v", rerr)
	}
	if got.Depth != 1 || got.ParentID != nil {
		t.Fatalf("a refused sink must move nothing, got %+v", got)
	}
}

// unreadableTopic writes a topic and then replaces its payload with one that will
// not decode, which is what the enumeration passes below must refuse to be without.
func unreadableTopic(t *testing.T, engine *core.StorageEngine, topic core.TopicSlot) {
	t.Helper()
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, topic.ID, &topic); err != nil {
		t.Fatalf("write topic %d: %v", topic.ID, err)
	}
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL2Topic, topic.ID, []byte(`{"id":`)); err != nil {
		t.Fatalf("make topic %d unreadable: %v", topic.ID, err)
	}
}

func writeTopic(t *testing.T, engine *core.StorageEngine, topic core.TopicSlot) {
	t.Helper()
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, topic.ID, &topic); err != nil {
		t.Fatalf("write topic %d: %v", topic.ID, err)
	}
}

// A child the closure cannot read is still that parent's child: dropping it from
// the list deletes the parent above a topic nobody will ever cascade again.
func TestTopicClosureL2RefusesUnreadableChild(t *testing.T) {
	engine := tempEngine(t)
	var root uint64 = 1
	writeTopic(t, engine, core.TopicSlot{ID: root, SceneID: 7, Depth: 1, FusedKeywords: []string{"k"}})
	unreadableTopic(t, engine, core.TopicSlot{ID: 2, SceneID: 7, Depth: 2, ParentID: &root})

	if _, err := TopicClosureL2(engine, core.DefaultAgentID, root); common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("a closure with a member it cannot read must be reported, got %v", err)
	}
}

// A scene cascade tombstones what this enumeration returns, so a topic of the scene
// that will not read back has to be reported, not dropped: the deleted scene would
// otherwise leave a topic naming nothing, listed by nobody and deleted by nobody.
func TestTopicIDsBySceneL2RefusesUnreadableTopic(t *testing.T) {
	engine := tempEngine(t)
	const sceneID = uint64(7)
	writeTopic(t, engine, core.TopicSlot{ID: 11, SceneID: sceneID, Depth: 1, FusedKeywords: []string{"k"}})
	unreadableTopic(t, engine, core.TopicSlot{ID: 12, SceneID: sceneID, Depth: 1, FusedKeywords: []string{"k"}})
	if err := core.WriteSceneSlot(engine, core.DefaultAgentID, sceneID, &core.SceneSlot{SceneID: sceneID}); err != nil {
		t.Fatalf("write scene: %v", err)
	}

	ids, err := TopicIDsBySceneL2(engine, core.DefaultAgentID, sceneID)
	if common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("the enumeration must report the topic it could not read, got %v", err)
	}
	if ids != nil {
		t.Fatalf("a refused enumeration returns no list to delete from, got %v", ids)
	}
}

// A merge sees the domain once, before it writes anything: a topic it could not
// read must leave the sibling on its own scene and that scene still there. Moving
// what did read and then refusing would hand the primary a topic set its own record
// no longer describes.
func TestMergeScenesL2RefusesUnreadableTopic(t *testing.T) {
	engine := tempEngine(t)
	const (
		primary   = uint64(7)
		secondary = uint64(8)
	)
	writeTopic(t, engine, core.TopicSlot{ID: 21, SceneID: secondary, Depth: 1, FusedKeywords: []string{"k"}})
	unreadableTopic(t, engine, core.TopicSlot{ID: 22, SceneID: secondary, Depth: 1, FusedKeywords: []string{"k"}})
	if err := core.WriteSceneSlot(engine, core.DefaultAgentID, secondary, &core.SceneSlot{SceneID: secondary}); err != nil {
		t.Fatalf("write scene: %v", err)
	}

	err := MergeScenesL2(engine, core.DefaultAgentID, primary, []uint64{secondary})
	if common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("the merge must report the topic it could not enumerate, got %v", err)
	}
	got, rerr := core.ReadTopicSlot(engine, core.DefaultAgentID, 21)
	if rerr != nil {
		t.Fatalf("read the untouched sibling: %v", rerr)
	}
	if got.SceneID != secondary {
		t.Fatalf("a refused merge must retarget nothing, got scene %d", got.SceneID)
	}
	if _, rerr := core.ReadSceneSlot(engine, core.DefaultAgentID, secondary); rerr != nil {
		t.Fatalf("a refused merge must delete no scene record: %v", rerr)
	}
}
