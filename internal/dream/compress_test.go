// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/config"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// anyKeywords answers every extraction with one keyword track, so a group is
// rejected only for what the engine itself can object to.
type anyKeywords struct{}

func (anyKeywords) Chat(context.Context, string, string, int) (string, error) {
	return `{"keywords":["登录","token"]}`, nil
}

func (c anyKeywords) ChatWithRetry(ctx context.Context, system, user string, _, _ int) (string, error) {
	return c.Chat(ctx, system, user, 0)
}

func (anyKeywords) MaxOutputTokens() int { return llmops.ConsolidationMaxTokens }

// The model groups by conversation thread and two adjacent threads can both
// claim one turn; applying both would sink that turn twice. What that leaves
// behind is the scene showing the turn's own originals one level below every
// read that reaches it, under a first summary that no longer holds it, beside a
// second summary standing over a single remaining member.
func TestApplyGroupsRejectsOverlappingGroups(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "test.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	const sceneID = uint64(7)
	turnIDs := []uint64{11, 12, 13}
	topics := make([]core.TopicSlot, 0, len(turnIDs))
	for i, id := range turnIDs {
		topic := core.TopicSlot{
			ID: id, SceneID: sceneID, Depth: 1,
			FusedKeywords: []string{"原文"}, UserTimestamp: int64(1000 + i), AgentTimestamp: int64(2000 + i),
		}
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, id, &topic); err != nil {
			t.Fatalf("write topic %d: %v", id, err)
		}
		topics = append(topics, topic)
	}
	ac := domain.NewContext(core.DefaultAgentID, context.Background(), engine, anyKeywords{}, &config.MemHopDefaults{})

	firstParent := core.ComputeTopicID(sceneID, 1000, 2001)
	secondParent := core.ComputeTopicID(sceneID, 1001, 2002)
	out := &llmops.ConsolidationOutput{L2Groups: []llmops.L2Group{
		{NodeHashes: []uint64{11, 12}, MergedSummary: "两轮把登录链路讲完"},
		{NodeHashes: []uint64{12, 13}, MergedSummary: "两轮都在追同一个 token 问题"},
	}}

	applied, rejected := applyGroups(context.Background(), ac, sceneID, topics, out)
	if applied != 1 || rejected != 1 {
		t.Fatalf("one group must land and the overlapping one must be refused, got applied=%d rejected=%d", applied, rejected)
	}

	shared, err := core.ReadTopicSlot(engine, core.DefaultAgentID, 12)
	if err != nil {
		t.Fatalf("read the shared member: %v", err)
	}
	if shared.Depth != 2 || shared.ParentID == nil || *shared.ParentID != firstParent {
		t.Fatalf("the shared turn sank twice or into the wrong group: depth=%d parent=%v", shared.Depth, shared.ParentID)
	}
	member, err := core.ReadTopicSlot(engine, core.DefaultAgentID, 11)
	if err != nil {
		t.Fatalf("read the first member: %v", err)
	}
	if member.Depth != 2 {
		t.Fatalf("an applied member sits one level under the surface, got depth %d", member.Depth)
	}
	untouched, err := core.ReadTopicSlot(engine, core.DefaultAgentID, 13)
	if err != nil {
		t.Fatalf("read the turn the refused group wanted: %v", err)
	}
	if untouched.Depth != 1 || untouched.ParentID != nil {
		t.Fatalf("a refused group must move nothing: depth=%d parent=%v", untouched.Depth, untouched.ParentID)
	}
	parent, err := core.ReadTopicSlot(engine, core.DefaultAgentID, firstParent)
	if err != nil {
		t.Fatalf("read the fused parent: %v", err)
	}
	if parent.Depth != 1 {
		t.Fatalf("the fused parent must be on the surface, got depth %d", parent.Depth)
	}
	if refused, err := core.ReadTopicLenient(engine, core.DefaultAgentID, secondParent); common.CodeOf(err) != common.ErrNotFound || refused != nil {
		t.Fatalf("the refused group left a fused parent behind: %+v err=%v", refused, err)
	}
}

// Members can be disjoint and still collide: the parent id is the group's
// timestamp bounds, and a host that stamps several turns with one timestamp gives
// two groups the same bounds. Only the first may land — otherwise one parent record
// ends up summarising one group while the other group's originals hang beneath it.
func TestApplyGroupsRefusesCollidingParentID(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "test.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	const sceneID = uint64(7)
	turnIDs := []uint64{21, 22, 23, 24}
	topics := make([]core.TopicSlot, 0, len(turnIDs))
	for _, id := range turnIDs {
		topic := core.TopicSlot{
			ID: id, SceneID: sceneID, Depth: 1,
			FusedKeywords: []string{"原文"}, UserTimestamp: 1000, AgentTimestamp: 2000,
		}
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, id, &topic); err != nil {
			t.Fatalf("write topic %d: %v", id, err)
		}
		topics = append(topics, topic)
	}
	ac := domain.NewContext(core.DefaultAgentID, context.Background(), engine, anyKeywords{}, &config.MemHopDefaults{})

	out := &llmops.ConsolidationOutput{L2Groups: []llmops.L2Group{
		{NodeHashes: []uint64{21, 22}, MergedSummary: "第一组：登录链路"},
		{NodeHashes: []uint64{23, 24}, MergedSummary: "第二组：完全不同的话题，但时间界一模一样"},
	}}
	applied, rejected := applyGroups(context.Background(), ac, sceneID, topics, out)
	if applied != 1 || rejected != 1 {
		t.Fatalf("the colliding group must be refused, got applied=%d rejected=%d", applied, rejected)
	}

	collisionParent := core.ComputeTopicID(sceneID, 1000, 2000)
	parent, err := core.ReadTopicSlot(engine, core.DefaultAgentID, collisionParent)
	if err != nil {
		t.Fatalf("read the landed parent: %v", err)
	}
	if parent.Depth != 1 {
		t.Fatalf("a fused parent belongs to the surface, got depth %d", parent.Depth)
	}
	for _, id := range []uint64{23, 24} {
		got, err := core.ReadTopicSlot(engine, core.DefaultAgentID, id)
		if err != nil {
			t.Fatalf("read %d: %v", id, err)
		}
		if got.Depth != 1 || got.ParentID != nil {
			t.Fatalf("the refused group moved %d: depth=%d parent=%v", id, got.Depth, got.ParentID)
		}
	}
	summary, err := core.ReadArchiveSlot(engine, core.DefaultAgentID,
		core.HashContent(collisionParent, core.SeqUser))
	if err != nil {
		t.Fatalf("read the parent's summary slot: %v", err)
	}
	if summary.Content != "第一组：登录链路" {
		t.Fatalf("the landed parent must still carry the first group's summary, got %q", summary.Content)
	}
}

// A member that will not read back fails the sink after the parent and its summary
// are already on disk, so the rollback is what keeps the scene from gaining a
// surface topic summarising turns that never moved. Undoing it by id is the point:
// a rollback that enumerated the domain would be refused by this very record and
// leave the half-applied group exactly where it is.
func TestApplyGroupsRollsBackTheGroupWhenASinkRefuses(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "test.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	const sceneID = uint64(7)
	topics := make([]core.TopicSlot, 0, 2)
	for i, id := range []uint64{31, 32} {
		topic := core.TopicSlot{
			ID: id, SceneID: sceneID, Depth: 1,
			FusedKeywords: []string{"原文"}, UserTimestamp: int64(1000 + i), AgentTimestamp: int64(2000 + i),
		}
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, id, &topic); err != nil {
			t.Fatalf("write topic %d: %v", id, err)
		}
		topics = append(topics, topic)
	}
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL2Topic, 32, []byte(`{"id":`)); err != nil {
		t.Fatalf("make the second member unreadable: %v", err)
	}
	ac := domain.NewContext(core.DefaultAgentID, context.Background(), engine, anyKeywords{}, &config.MemHopDefaults{})

	out := &llmops.ConsolidationOutput{L2Groups: []llmops.L2Group{
		{NodeHashes: []uint64{31, 32}, MergedSummary: "两轮把登录链路讲完"},
	}}
	applied, rejected := applyGroups(context.Background(), ac, sceneID, topics, out)
	if applied != 0 || rejected != 1 {
		t.Fatalf("a group whose sink refused must be rejected, got applied=%d rejected=%d", applied, rejected)
	}

	parentID := core.ComputeTopicID(sceneID, 1000, 2001)
	if stored, err := core.ReadTopicLenient(engine, core.DefaultAgentID, parentID); common.CodeOf(err) != common.ErrNotFound || stored != nil {
		t.Fatalf("the rolled-back group left its parent behind: %+v err=%v", stored, err)
	}
	if _, err := core.ReadArchiveSlot(engine, core.DefaultAgentID, core.HashContent(parentID, core.SeqUser)); common.CodeOf(err) != common.ErrNotFound {
		t.Fatalf("and its summary content: err=%v", err)
	}
	member, err := core.ReadTopicSlot(engine, core.DefaultAgentID, 31)
	if err != nil {
		t.Fatalf("read the member the sink never reached: %v", err)
	}
	if member.Depth != 1 || member.ParentID != nil {
		t.Fatalf("a rejected group moves no member: depth=%d parent=%v", member.Depth, member.ParentID)
	}
}

// A group the engine cannot see whole is not a group it may fuse. The listing handed
// to the model is the only source of these ids, so a name it invented (or one whose
// topic has since gone) would otherwise contribute nothing to the bounds the parent
// is keyed by, and the sink step would drop it without a word: a summary of two
// turns left standing over one.
func TestApplyGroupsRefusesAGroupItCannotSeeWhole(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "test.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	const sceneID = uint64(7)
	seen := core.TopicSlot{
		ID: 41, SceneID: sceneID, Depth: 1, FusedKeywords: []string{"原文"},
		UserTimestamp: 1000, AgentTimestamp: 2001,
	}
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, 41, &seen); err != nil {
		t.Fatalf("write topic: %v", err)
	}
	ac := domain.NewContext(core.DefaultAgentID, context.Background(), engine, anyKeywords{}, &config.MemHopDefaults{})

	out := &llmops.ConsolidationOutput{L2Groups: []llmops.L2Group{
		{NodeHashes: []uint64{41, 42}, MergedSummary: "两轮的内容，其中一轮引擎看不见"},
	}}
	applied, rejected := applyGroups(context.Background(), ac, sceneID, []core.TopicSlot{seen}, out)
	if applied != 0 || rejected != 1 {
		t.Fatalf("a group with an unseen member must be refused, got applied=%d rejected=%d", applied, rejected)
	}
	if stored, err := core.ReadTopicLenient(engine, core.DefaultAgentID,
		core.ComputeTopicID(sceneID, 1000, 2001)); common.CodeOf(err) != common.ErrNotFound || stored != nil {
		t.Fatalf("a refused group leaves no fused parent: %+v err=%v", stored, err)
	}
	member, err := core.ReadTopicSlot(engine, core.DefaultAgentID, 41)
	if err != nil {
		t.Fatalf("read the member: %v", err)
	}
	if member.Depth != 1 || member.ParentID != nil {
		t.Fatalf("a refused group sinks nothing: depth=%d parent=%v", member.Depth, member.ParentID)
	}
}

// A one-name group is a proposal this engine cannot apply — a fused parent exists to
// stand over children — and it is a different fact from "nothing left to
// consolidate". Skipping it without a word makes the report read as a scene nobody
// asked to merge, which is exactly the case this pass must not be confused with.
func TestApplyGroupsCountsADegenerateGroupAsProposedButUnapplied(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "test.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	const sceneID = uint64(7)
	alone := core.TopicSlot{
		ID: 51, SceneID: sceneID, Depth: 1, FusedKeywords: []string{"原文"},
		UserTimestamp: 1000, AgentTimestamp: 2001,
	}
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, 51, &alone); err != nil {
		t.Fatalf("write topic: %v", err)
	}
	ac := domain.NewContext(core.DefaultAgentID, context.Background(), engine, anyKeywords{}, &config.MemHopDefaults{})

	out := &llmops.ConsolidationOutput{L2Groups: []llmops.L2Group{
		{NodeHashes: []uint64{51}, MergedSummary: "一轮自己算一组"},
	}}
	applied, rejected := applyGroups(context.Background(), ac, sceneID, []core.TopicSlot{alone}, out)
	if applied != 0 || rejected != 1 {
		t.Fatalf("a degenerate group is proposed-but-unapplied, got applied=%d rejected=%d", applied, rejected)
	}
	member, err := core.ReadTopicSlot(engine, core.DefaultAgentID, 51)
	if err != nil {
		t.Fatalf("read the member: %v", err)
	}
	if member.Depth != 1 || member.ParentID != nil {
		t.Fatalf("a degenerate group sinks nothing: depth=%d parent=%v", member.Depth, member.ParentID)
	}
}

// A fused parent is a topic like any other, so the next pass can hand it back as a
// group member: it sinks to depth 2 and ends level with the turns it summarizes.
// Depth therefore reports surface (1) versus swallowed (2), never "summary" versus
// "turn" — and nothing falls out of the depth-2 window SceneContext reads, so the
// originals under a folded group are still the only place they are said.
func TestAFusedParentFoldsIntoALaterGroup(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "test.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	const sceneID = uint64(7)
	writeTurn := func(id uint64, userTS int64) core.TopicSlot {
		topic := core.TopicSlot{
			ID: id, SceneID: sceneID, Depth: 1, FusedKeywords: []string{"原文"},
			UserTimestamp: userTS, AgentTimestamp: userTS + 1000,
		}
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, id, &topic); err != nil {
			t.Fatalf("write topic %d: %v", id, err)
		}
		return topic
	}
	ac := domain.NewContext(core.DefaultAgentID, context.Background(), engine, anyKeywords{}, &config.MemHopDefaults{})
	ctx := context.Background()

	first := []core.TopicSlot{writeTurn(61, 1000), writeTurn(62, 1001)}
	if applied, rejected := applyGroups(ctx, ac, sceneID, first, &llmops.ConsolidationOutput{
		L2Groups: []llmops.L2Group{{NodeHashes: []uint64{61, 62}, MergedSummary: "两轮讲完登录链路"}},
	}); applied != 1 || rejected != 0 {
		t.Fatalf("first pass: applied=%d rejected=%d", applied, rejected)
	}
	parent, err := core.ReadTopicSlot(engine, core.DefaultAgentID, core.ComputeTopicID(sceneID, 1000, 2001))
	if err != nil {
		t.Fatalf("read the fused parent: %v", err)
	}
	if parent.Depth != 1 {
		t.Fatalf("a fresh fused parent belongs to the surface, got depth %d", parent.Depth)
	}

	second := []core.TopicSlot{*parent, writeTurn(63, 1002)}
	if applied, rejected := applyGroups(ctx, ac, sceneID, second, &llmops.ConsolidationOutput{
		L2Groups: []llmops.L2Group{{NodeHashes: []uint64{parent.ID, 63}, MergedSummary: "三轮都在追同一个 token"}},
	}); applied != 1 || rejected != 0 {
		t.Fatalf("second pass: applied=%d rejected=%d", applied, rejected)
	}

	folded, err := core.ReadTopicSlot(engine, core.DefaultAgentID, parent.ID)
	if err != nil {
		t.Fatalf("read the folded parent: %v", err)
	}
	if folded.Depth != 2 || folded.ParentID == nil {
		t.Fatalf("a later pass must be able to swallow a fused parent: depth=%d parent=%v",
			folded.Depth, folded.ParentID)
	}
	for _, id := range []uint64{61, 62} {
		child, err := core.ReadTopicSlot(engine, core.DefaultAgentID, id)
		if err != nil {
			t.Fatalf("read %d: %v", id, err)
		}
		if child.Depth != folded.Depth || child.ParentID == nil || *child.ParentID != parent.ID {
			t.Fatalf("the folded group's own children must stay where they were, level with it: depth=%d parent=%v",
				child.Depth, child.ParentID)
		}
	}
}
