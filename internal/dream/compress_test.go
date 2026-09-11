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
		{SceneID: sceneID, NodeHashes: []uint64{11, 12}, MergedSummary: "两轮把登录链路讲完"},
		{SceneID: sceneID, NodeHashes: []uint64{12, 13}, MergedSummary: "两轮都在追同一个 token 问题"},
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
