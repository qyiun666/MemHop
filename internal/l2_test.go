// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// mustScene writes a scene record under a host-chosen id and returns it.
func mustScene(t *testing.T, engine *core.StorageEngine, sceneID uint64, name string) core.SceneSlot {
	t.Helper()
	slot := core.NewSceneSlot(sceneID, name)
	if err := core.WriteSceneSlot(engine, core.DefaultAgentID, sceneID, &slot); err != nil {
		t.Fatalf("write scene %d: %v", sceneID, err)
	}
	return slot
}

// TestListScenesEmpty empty db returns an empty slice.
func TestListScenesEmpty(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	scenes, err := db.ListScenes(core.DefaultAgentID, "")
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	if len(scenes) != 0 {
		t.Fatalf("want 0 scenes, got %d", len(scenes))
	}
}

// TestListScenesReturnsIDName multiple scenes return scene_id + scene_name.
func TestListScenesReturnsIDName(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	s1 := mustScene(t, engine, 11, "工作")
	s2 := mustScene(t, engine, 12, "学习")

	scenes, err := db.ListScenes(core.DefaultAgentID, "")
	if err != nil {
		t.Fatalf("ListScenes: %v", err)
	}
	if len(scenes) != 2 {
		t.Fatalf("want 2 scenes, got %d", len(scenes))
	}
	byID := make(map[uint64]string, len(scenes))
	for _, s := range scenes {
		byID[s.SceneID] = s.SceneName
	}
	if byID[s1.SceneID] != "工作" || byID[s2.SceneID] != "学习" {
		t.Fatalf("unexpected scenes: %v", byID)
	}
}

// TestMergeScenesMovesTopics topics move to primary, secondary deleted, primary kept.
func TestMergeScenesMovesTopics(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	primary := mustScene(t, engine, 21, "主场景")
	secondary := mustScene(t, engine, 22, "副场景")

	t1 := newTopic(common.HashID("t1"), secondary.SceneID, 1000, []string{"a"})
	t2 := newTopic(common.HashID("t2"), secondary.SceneID, 2000, []string{"b"})
	for _, tp := range []core.TopicSlot{t1, t2} {
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, tp.ID, &tp); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.MergeScenes(core.DefaultAgentID, common.FormatHash(primary.SceneID),
		[]string{common.FormatHash(secondary.SceneID)}); err != nil {
		t.Fatalf("MergeScenes: %v", err)
	}
	if _, err := core.ReadSceneSlot(engine, core.DefaultAgentID, secondary.SceneID); err == nil {
		t.Fatal("secondary scene should be deleted")
	}
	if _, err := core.ReadSceneSlot(engine, core.DefaultAgentID, primary.SceneID); err != nil {
		t.Fatal("primary scene should remain")
	}
	for _, id := range []uint64{t1.ID, t2.ID} {
		topics, err := core.ReadTopicSlot(engine, core.DefaultAgentID, id)
		if err != nil {
			t.Fatal(err)
		}
		if topics == nil || topics.SceneID != primary.SceneID {
			t.Fatalf("topic %d scene: want %d", id, primary.SceneID)
		}
	}
}

// Merging also retargets the cached entries, otherwise the scene read keeps
// serving topics under a scene id that no longer exists.
func TestMergeScenesRetargetsCache(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	primary := mustScene(t, engine, 31, "主场景")
	secondary := mustScene(t, engine, 32, "副场景")
	topic := newTopic(common.HashID("cached"), secondary.SceneID, 1000, []string{"a"})
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, topic.ID, &topic); err != nil {
		t.Fatal(err)
	}
	ac := testDefaultContext(db)
	ac.SyncL2Meta(&topic)

	if err := db.MergeScenes(core.DefaultAgentID, common.FormatHash(primary.SceneID),
		[]string{common.FormatHash(secondary.SceneID)}); err != nil {
		t.Fatalf("MergeScenes: %v", err)
	}
	if got := ac.L2Meta.Get(topic.ID); got == nil || got.SceneID != primary.SceneID {
		t.Fatalf("cache not retargeted: %+v", got)
	}
	if ids := ac.L2Meta.TopicsByScene(secondary.SceneID); len(ids) != 0 {
		t.Fatalf("merged scene still cached: %v", ids)
	}
}

// A merged-away scene's L1 node has to go with it. The merge retargets the scene's
// topics onto the primary, so nothing names that node again — and the rebuild
// decides staleness from a node's own topics, which still read back — so a ghost
// node keeps its importance, keeps being distilled into the profile, and keeps
// pairing with live scenes in every later hyperedge pass.
func TestMergeScenesRemovesTheMergedSceneNode(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	primary := mustScene(t, engine, 51, "主场景")
	secondary := mustScene(t, engine, 52, "副场景")
	topic := newTopic(common.HashID("kept"), secondary.SceneID, 1000, []string{"a"})
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, topic.ID, &topic); err != nil {
		t.Fatal(err)
	}
	for _, sceneID := range []uint64{primary.SceneID, secondary.SceneID} {
		nodeID := core.SceneNodeID(sceneID)
		if err := core.WriteSceneNode(engine, core.DefaultAgentID, nodeID, &core.SceneNode{
			IDHash: nodeID, SceneID: sceneID, TopicIDs: []uint64{topic.ID}, CreatedAt: 1000, Importance: 1.0,
		}); err != nil {
			t.Fatalf("write scene node %d: %v", sceneID, err)
		}
	}

	if err := db.MergeScenes(core.DefaultAgentID, common.FormatHash(primary.SceneID),
		[]string{common.FormatHash(secondary.SceneID)}); err != nil {
		t.Fatalf("MergeScenes: %v", err)
	}
	if _, err := core.ReadSceneNode(engine, core.DefaultAgentID, core.SceneNodeID(secondary.SceneID)); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("the merged-away scene still holds an L1 node: %v", err)
	}
	if _, err := core.ReadSceneNode(engine, core.DefaultAgentID, core.SceneNodeID(primary.SceneID)); err != nil {
		t.Fatalf("the primary's own node must survive: %v", err)
	}
}

// TestMergeScenesInvalid invalid primary ID and empty secondary list error.
func TestMergeScenesInvalid(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	if err := db.MergeScenes(core.DefaultAgentID, "nothex", []string{"abc"}); err == nil {
		t.Fatal("want error for invalid primary id")
	}
	if err := db.MergeScenes(core.DefaultAgentID, common.FormatHash(1), nil); err == nil {
		t.Fatal("want error for empty secondary ids")
	}
}

// TestMergeScenesPrimaryInSecondary primary must never be deleted by a merge.
func TestMergeScenesPrimaryInSecondary(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	primary := mustScene(t, engine, 41, "主场景")
	secondary := mustScene(t, engine, 42, "副场景")
	t1 := newTopic(common.HashID("t1"), primary.SceneID, 1000, []string{"a"})
	t2 := newTopic(common.HashID("t2"), secondary.SceneID, 2000, []string{"b"})
	for _, tp := range []core.TopicSlot{t1, t2} {
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, tp.ID, &tp); err != nil {
			t.Fatal(err)
		}
	}

	// primary listed among secondaries -> rejected, nothing deleted.
	if err := db.MergeScenes(core.DefaultAgentID, common.FormatHash(primary.SceneID), []string{
		common.FormatHash(primary.SceneID), common.FormatHash(secondary.SceneID),
	}); err == nil {
		t.Fatal("want error when primary is also a secondary")
	}
	for _, s := range []core.SceneSlot{primary, secondary} {
		if _, err := core.ReadSceneSlot(engine, core.DefaultAgentID, s.SceneID); err != nil {
			t.Fatalf("scene %d must survive a rejected merge", s.SceneID)
		}
	}
	for _, id := range []uint64{t1.ID, t2.ID} {
		if _, err := core.ReadTopicSlot(engine, core.DefaultAgentID, id); err != nil {
			t.Fatalf("topic %d must remain after rejected merge", id)
		}
	}
}

// TestDeleteTopicRemovesSubtreeAndArchives deleting a topic removes its
// subtree, the L4 archives it owns, and its L2Meta entries.
func TestDeleteTopicRemovesSubtreeAndArchives(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	ac := testDefaultContext(db)
	scene := mustScene(t, engine, 61, "工作")

	parentID := common.HashID("parent")
	childID := common.HashID("child")
	arcID := core.HashContent(parentID, core.SeqUser)
	parent := newTopic(parentID, scene.SceneID, 1000, []string{"a"})
	child := newTopic(childID, scene.SceneID, 2000, []string{"b"})
	child.ParentID = &parentID
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, parentID, &parent); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, childID, &child); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteArchiveSlot(engine, core.DefaultAgentID, arcID, &core.ArchiveSlot{
		IDHash: arcID, Kind: core.KindUtterance, Seq: core.SeqUser,
		TopicID: parentID, Content: "原文", CreatedAt: 1500,
	}); err != nil {
		t.Fatal(err)
	}
	ac.L4.Append(parentID, core.SeqUser, arcID, core.KindUtterance, 1500)
	ac.L2Meta.Update(index.L2MetaFromTopic(&parent))
	ac.L2Meta.Update(index.L2MetaFromTopic(&child))

	if err := db.DeleteTopic(core.DefaultAgentID, common.FormatHash(parentID)); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	for _, id := range []uint64{parentID, childID} {
		if topics, err := core.ReadTopicSlot(engine, core.DefaultAgentID, id); err == nil && topics != nil {
			t.Errorf("topic %d should be deleted", id)
		}
		if ac.L2Meta.Get(id) != nil {
			t.Errorf("l2meta entry %d should be removed", id)
		}
	}
	if arcs, err := core.ReadArchiveSlot(engine, core.DefaultAgentID, arcID); err == nil && arcs != nil {
		t.Error("archive should be deleted")
	}
	// The scene record survives a topic deletion.
	if _, err := core.ReadSceneSlot(engine, core.DefaultAgentID, scene.SceneID); err != nil {
		t.Error("scene should survive topic deletion")
	}
}

// TestDeleteTopicNotFound missing topic returns ErrNotFound.
func TestDeleteTopicNotFound(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	if err := db.DeleteTopic(core.DefaultAgentID, common.FormatHash(common.HashID("ghost"))); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestDeleteSceneNotFound missing scene returns ErrNotFound (consistent
// with DeleteTopic).
func TestDeleteSceneNotFound(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	if err := db.DeleteScene(core.DefaultAgentID, common.FormatHash(common.HashID("ghost-scene"))); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// Every entry that takes a topic key parses it the same way, so the reserved
// all-zero one is refused by the two that neither write nor read a record either:
// naming a topic no read can ever serve is not a lookup that misses, it is an id
// the library never issues.
func TestTopicKeyEntryPointsRejectReservedZero(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	const zero = "0000000000000000"
	if _, err := db.RenameTopic(core.DefaultAgentID, zero, "名字"); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("RenameTopic on the zero key: want ErrInvalidQuery, got %v", err)
	}
	if err := db.DeleteTopic(core.DefaultAgentID, zero); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("DeleteTopic on the zero key: want ErrInvalidQuery, got %v", err)
	}
}

// TestDeleteTopicSubtreeComesFromParentID a topic names no children of its own;
// the tree is the children's ParentID. So deleting one turn leaves the parent and
// its other children standing, and the scene read still counts exactly the
// children that remain.
func TestDeleteTopicSubtreeComesFromParentID(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	ac := testDefaultContext(db)
	scene := mustScene(t, engine, 71, "工作")

	parentID := common.HashID("parent")
	childID := common.HashID("child")
	siblingID := common.HashID("sibling")
	parent := newTopic(parentID, scene.SceneID, 1000, []string{"a"})
	kids := []core.TopicSlot{
		newTopic(childID, scene.SceneID, 2000, []string{"b"}),
		newTopic(siblingID, scene.SceneID, 3000, []string{"c"}),
	}
	for i := range kids {
		kids[i].Depth = 2
		kids[i].ParentID = &parentID
	}
	for _, topic := range append([]core.TopicSlot{parent}, kids...) {
		write := topic
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, write.ID, &write); err != nil {
			t.Fatal(err)
		}
		ac.L2Meta.Update(index.L2MetaFromTopic(&write))
	}

	if err := db.DeleteTopic(core.DefaultAgentID, common.FormatHash(childID)); err != nil {
		t.Fatalf("DeleteTopic: %v", err)
	}
	if _, err := core.ReadTopicSlot(engine, core.DefaultAgentID, childID); err == nil {
		t.Error("the named child should be gone")
	}
	ctx, err := db.SceneContext(core.DefaultAgentID, common.FormatHash(scene.SceneID))
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}
	var parentView, siblingView *core.SceneContextTopic
	for i := range ctx.Topics {
		switch ctx.Topics[i].TopicID {
		case common.FormatHash(parentID):
			parentView = &ctx.Topics[i]
		case common.FormatHash(siblingID):
			siblingView = &ctx.Topics[i]
		}
	}
	if parentView == nil || siblingView == nil {
		t.Fatalf("deleting one child lost the rest of the tree: %+v", ctx.Topics)
	}
	if parentView.ChildCount != 1 {
		t.Fatalf("parent reports %d children, want the one still on disk", parentView.ChildCount)
	}
}

// TestDeleteSceneRemovesEverything deleting a scene removes its record, all
// topics (all depths), archives and their L2Meta entries.
func TestDeleteSceneRemovesEverything(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	ac := testDefaultContext(db)
	scene := mustScene(t, engine, 3, "工作")

	t1 := newTopic(common.HashID("t1"), scene.SceneID, 1000, []string{"a"})
	t2 := newTopic(common.HashID("t2"), scene.SceneID, 2000, []string{"b"})
	t3 := newTopic(common.HashID("t3"), scene.SceneID, 3000, []string{"c"})
	t3.ParentID = &t2.ID
	arcID := core.HashContent(t1.ID, core.SeqUser)
	if err := core.WriteArchiveSlot(engine, core.DefaultAgentID, arcID, &core.ArchiveSlot{
		IDHash: arcID, Kind: core.KindUtterance, Seq: core.SeqUser,
		TopicID: t1.ID, Content: "原文", CreatedAt: 1500,
	}); err != nil {
		t.Fatal(err)
	}
	ac.L4.Append(t1.ID, core.SeqUser, arcID, core.KindUtterance, 1500)
	for _, topic := range []core.TopicSlot{t1, t2, t3} {
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, topic.ID, &topic); err != nil {
			t.Fatal(err)
		}
		ac.L2Meta.Update(index.L2MetaFromTopic(&topic))
	}

	if err := db.DeleteScene(core.DefaultAgentID, common.FormatHash(scene.SceneID)); err != nil {
		t.Fatalf("DeleteScene: %v", err)
	}
	if _, err := core.ReadSceneSlot(engine, core.DefaultAgentID, scene.SceneID); err == nil {
		t.Error("scene record should be deleted")
	}
	for _, topic := range []core.TopicSlot{t1, t2, t3} {
		if topics, err := core.ReadTopicSlot(engine, core.DefaultAgentID, topic.ID); err == nil && topics != nil {
			t.Errorf("topic %d should be deleted", topic.ID)
		}
		if ac.L2Meta.Get(topic.ID) != nil {
			t.Errorf("l2meta entry %d should be removed", topic.ID)
		}
	}
	if arcs, err := core.ReadArchiveSlot(engine, core.DefaultAgentID, arcID); err == nil && arcs != nil {
		t.Error("archive should be deleted")
	}
}

// The cascade's last whole-bucket enumeration runs before its first tombstone, so a
// node that will not read back — even one belonging to another turn — stops the
// delete with everything in place. Tombstoning the scene and its topics first would
// leave a turn whose tree is still on disk, and a retry could never see that the
// deletion it is being asked for already happened.
func TestDeleteSceneRefusesWhileThePlanBucketIsUnreadable(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	ac := testDefaultContext(db)
	scene := mustScene(t, engine, 3, "工作")
	t1 := newTopic(common.HashID("t1"), scene.SceneID, 1000, []string{"a"})
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, t1.ID, &t1); err != nil {
		t.Fatal(err)
	}
	ac.L2Meta.Update(index.L2MetaFromTopic(&t1))

	const otherTurn = uint64(99)
	dangling := core.HashPlanNode(otherTurn, 1)
	if err := core.WritePlanNode(engine, core.DefaultAgentID, dangling,
		&core.PlanNode{IDHash: dangling, TopicID: otherTurn, Seq: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL5PlanNode, dangling, []byte(`{"id":`)); err != nil {
		t.Fatalf("make the node unreadable: %v", err)
	}

	if err := db.DeleteScene(core.DefaultAgentID, common.FormatHash(scene.SceneID)); common.CodeOf(err) != common.ErrDeserialization {
		t.Fatalf("the cascade must refuse on a bucket it could not enumerate, got %v", err)
	}
	if _, err := core.ReadSceneSlot(engine, core.DefaultAgentID, scene.SceneID); err != nil {
		t.Fatalf("a refused cascade deletes no scene record: %v", err)
	}
	if _, err := core.ReadTopicSlot(engine, core.DefaultAgentID, t1.ID); err != nil {
		t.Fatalf("nor any of its topics: %v", err)
	}
	if ac.L2Meta.Get(t1.ID) == nil {
		t.Fatal("the mirror is untouched until the disk agrees")
	}
}

// A scene is named by the library when it is created; UpdateScene is the
// host's only way to title one. The title must survive a later Search, which
// rewrites that very record to bump its hit counter and turn sequence.
func TestUpdateSceneNameSurvivesLaterTurns(t *testing.T) {
	srv, calls := countingLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)

	res, err := db.Search(core.DefaultAgentID, SearchQuery{})
	if err != nil {
		t.Fatalf("open scene: %v", err)
	}
	sceneHex := common.FormatHash(res.Scene.SceneID)
	appendTurn(t, db, res.Scene.SceneID, res.NewTopicID, 1000)
	if err := settle(db, res.Scene.SceneID, res.NewTopicID); err != nil {
		t.Fatalf("settle turn: %v", err)
	}
	title := "rust 学习"
	if _, err := db.UpdateScene(core.DefaultAgentID, sceneHex, ScenePatch{Name: &title}); err != nil {
		t.Fatalf("UpdateScene rename: %v", err)
	}
	again, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: sceneHex})
	if err != nil {
		t.Fatalf("reopen scene: %v", err)
	}
	if again.Scene.SceneName != "rust 学习" {
		t.Fatalf("name after reopen = %q, want the title to persist", again.Scene.SceneName)
	}
	if again.Scene.TurnSeq <= res.Scene.TurnSeq {
		t.Fatalf("reopen did not advance TurnSeq: %d -> %d", res.Scene.TurnSeq, again.Scene.TurnSeq)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("LLM calls = %d, want 1 (only Update distills)", got)
	}
	blank := ""
	if _, err := db.UpdateScene(core.DefaultAgentID, sceneHex, ScenePatch{Name: &blank}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Errorf("empty title: want ErrInvalidQuery, got %v", err)
	}
	ghost := "x"
	if _, err := db.UpdateScene(core.DefaultAgentID, common.FormatHash(common.HashID("ghost")), ScenePatch{Name: &ghost}); common.CodeOf(err) != common.ErrNotFound {
		t.Errorf("unknown scene: want ErrNotFound, got %v", err)
	}
	// A patch that names no field is a no-op, not a rewrite.
	if _, err := db.UpdateScene(core.DefaultAgentID, sceneHex, ScenePatch{}); err != nil {
		t.Fatalf("empty patch: %v", err)
	}
}

// The capability regression this layer accepts, proved end to end: once the
// retention window passes, a topic keeps the keyword track Dream folded out of it
// and its transcript comes back **empty, not failed** — an expired turn is a legal
// end state, and a host has to be able to tell that apart from a read that lost a
// line (which stays a hard ErrIO).
func TestSceneContextAfterContentRetentionIsEmptyNotAnError(t *testing.T) {
	srv := contractLLMServer(t)
	db := newSearchTestDB(t, srv.URL)
	sceneID, topicID := openTurn(t, db)

	appendTurn(t, db, sceneID, topicID, 1000)
	if err := settle(db, sceneID, topicID); err != nil {
		t.Fatalf("update: %v", err)
	}
	if owned := archivesOfTopic(t, db.engine, topicID); len(owned) != 2 {
		t.Fatalf("a settled turn should own its two originals, got %d", len(owned))
	}

	// The appended originals sit at 1000/2000 ms since the epoch, so they are
	// already far outside any 7-day window: one Dream pass is the whole expiry
	// path.
	if _, err := db.RunDream(context.Background(), core.DefaultAgentID, 0); err != nil {
		t.Fatalf("dream: %v", err)
	}
	if owned := archivesOfTopic(t, db.engine, topicID); len(owned) != 0 {
		t.Fatalf("expired content survived the sweep: %d record(s)", len(owned))
	}

	cctx, err := db.SceneContext(core.DefaultAgentID, common.FormatHash(sceneID))
	if err != nil {
		t.Fatalf("an expired transcript must not fail the read: %v", err)
	}
	var found *SceneContextTopic
	for i := range cctx.Topics {
		if cctx.Topics[i].TopicID == common.FormatHash(topicID) {
			found = &cctx.Topics[i]
		}
	}
	if found == nil {
		t.Fatalf("the topic itself vanished from the scene context: %+v", cctx.Topics)
	}
	if len(found.Messages) != 0 {
		t.Fatalf("swept content still rendered: %+v", found.Messages)
	}
	// The keyword track is the durable product, and it is what survives: without
	// this the read would be indistinguishable from a scene that never had the
	// turn at all.
	if len(found.Keywords) == 0 {
		t.Fatalf("the topic lost the one thing that outlives its content: %+v", found)
	}
}

// SceneContext is the recovery read: it opens no turn. The turn counter is not
// on the host-visible scene record, so the contract is pinned here, where the
// record itself is readable. A read that consumed a turn would hand the host's
// next Search an id for a turn nobody opened, and the one it skipped would never
// settle.
func TestSceneContextOpensNoTurn(t *testing.T) {
	srv := mockLLMServer(t, turnKeywords)
	db := newSearchTestDB(t, srv.URL)
	sceneID, _ := openTurn(t, db)

	if _, err := db.SceneContext(core.DefaultAgentID, common.FormatHash(sceneID)); err != nil {
		t.Fatalf("scene context: %v", err)
	}
	slot, err := core.ReadSceneSlot(db.engine, core.DefaultAgentID, sceneID)
	if err != nil {
		t.Fatalf("read scene: %v", err)
	}
	if slot.TurnSeq != 1 {
		t.Fatalf("SceneContext opened a turn: TurnSeq = %d, want 1", slot.TurnSeq)
	}

	res, err := db.Search(core.DefaultAgentID, SearchQuery{SceneID: common.FormatHash(sceneID)})
	if err != nil {
		t.Fatalf("search after SceneContext: %v", err)
	}
	if want := core.ComputeTurnTopicID(sceneID, 2); res.NewTopicID != want {
		t.Fatalf("next read issued %d, want turn 2 (%d)", res.NewTopicID, want)
	}
}
