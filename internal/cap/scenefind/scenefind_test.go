// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package scenefind

import (
	"context"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// TestTopSceneRelevanceOrder: the hit scene's Topics must be ordered by
// fused relevance, not recency — an older matching topic ranks above a
// newer non-matching one.
func TestTopSceneRelevanceOrder(t *testing.T) {
	engine := newTestEngine(t)
	sparse := index.NewSparseIndex()
	// scene 1: old topic about rust (t=100), new topic about cooking (t=300).
	writeTopic(t, engine, sparse, core.DefaultAgentID, newTopic(4001, 1, 100, []string{"rust"}))
	writeTopic(t, engine, sparse, core.DefaultAgentID, newTopic(4002, 1, 300, []string{"cooking"}))

	hit, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, nil, "rust", []string{"rust"}, nil, 0, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if len(hit.Topics) != 2 {
		t.Fatalf("Topics = %d entries; want 2", len(hit.Topics))
	}
	if hit.Topics[0].Topic.ID != 4001 {
		t.Errorf("Topics[0] = %d; want 4001 (rust topic first by relevance, not recency)", hit.Topics[0].Topic.ID)
	}
	if hit.Topics[0].Score <= hit.Topics[1].Score {
		t.Errorf("Topics[0].Score = %v; want > Topics[1].Score = %v", hit.Topics[0].Score, hit.Topics[1].Score)
	}
}

// TestTopSceneBasic basic retrieval: the hit scene wins.
func TestTopSceneBasic(t *testing.T) {
	engine := newTestEngine(t)
	sparse := index.NewSparseIndex()
	writeTopic(t, engine, sparse, core.DefaultAgentID, newTopic(4001, 1, 100, []string{"rust"}))
	writeTopic(t, engine, sparse, core.DefaultAgentID, newTopic(4002, 2, 200, []string{"cooking"}))

	hit, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, nil, "rust", []string{"rust"}, nil, 0, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if hit.SceneID != 1 {
		t.Errorf("SceneID = %d; want 1", hit.SceneID)
	}
	if hit.Score <= 1.0 {
		t.Errorf("Score = %v; want > 1.0 (rrf + keyword hit)", hit.Score)
	}
}

// TestTopSceneAggregation scores of topics in the same scene are summed.
func TestTopSceneAggregation(t *testing.T) {
	engine := newTestEngine(t)
	sparse := index.NewSparseIndex()
	writeTopic(t, engine, sparse, core.DefaultAgentID, newTopic(4001, 1, 100, []string{"rust"}))
	writeTopic(t, engine, sparse, core.DefaultAgentID, newTopic(4002, 1, 200, []string{"rust"}))
	writeTopic(t, engine, sparse, core.DefaultAgentID, newTopic(4003, 2, 300, []string{"cooking"}))

	hit, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, nil, "rust", []string{"rust"}, nil, 0, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if hit.SceneID != 1 {
		t.Errorf("SceneID = %d; want 1", hit.SceneID)
	}
	// Two topics with hit=1.0 each, RRF non-negative -> scene total >= 2.0.
	if hit.Score < 2.0 {
		t.Errorf("Score = %v; want >= 2.0 (two topics summed)", hit.Score)
	}
}

// TestTopSceneActivationBonus active scene +0.2, duplicate activation adds once.
func TestTopSceneActivationBonus(t *testing.T) {
	engine := newTestEngine(t)
	sparse := index.NewSparseIndex()
	// Single topic: bm25 + entity channels each contribute 1/61, keyword hit = 1.0.
	writeTopic(t, engine, sparse, core.DefaultAgentID, newTopic(4001, 1, 100, []string{"rust"}))

	want := float32(1.0) + 2.0/61.0 + 0.2
	hit, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, nil, "rust", []string{"rust"}, []uint64{1, 1}, 1.15, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if hit.SceneID != 1 || !approx(hit.Score, want) {
		t.Errorf("hit = %+v; want SceneID=1 Score=%v (bonus added once)", hit, want)
	}

	// No active scene -> no 0.2, below the 1.15 threshold -> empty.
	empty, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, nil, "rust", []string{"rust"}, nil, 1.15, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if empty.SceneID != 0 || empty.Score != 0 {
		t.Errorf("expected empty hit, got %+v", empty)
	}
}

// TestTopSceneRecentBonus latest-timestamp scene +0.1, active takes priority.
func TestTopSceneRecentBonus(t *testing.T) {
	engine := newTestEngine(t)
	sparse := index.NewSparseIndex()
	writeTopic(t, engine, sparse, core.DefaultAgentID, newTopic(4001, 1, 100, []string{"rust"}))

	// Latest-topic scene +0.1: score = 1.0 + 2/61 (bm25+entity) + 0.1.
	recent, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, nil, "rust", []string{"rust"}, nil, 1.05, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	wantRecent := float32(1.0) + 2.0/61.0 + 0.1
	if recent.SceneID != 1 || !approx(recent.Score, wantRecent) {
		t.Errorf("recent hit = %+v; want Score=%v", recent, wantRecent)
	}

	// Same scene active -> active priority, only +0.2 not +0.1.
	active, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, nil, "rust", []string{"rust"}, []uint64{1}, 1.15, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	wantActive := float32(1.0) + 2.0/61.0 + 0.2
	if active.SceneID != 1 || !approx(active.Score, wantActive) {
		t.Errorf("active hit = %+v; want Score=%v (active takes priority)", active, wantActive)
	}
}

// TestTopSceneVectorChannel vector channel: cosine hit when the encoder is available, empty channel otherwise.
func TestTopSceneVectorChannel(t *testing.T) {
	engine := newTestEngine(t)
	sparse := index.NewSparseIndex()
	// Topic keywords do not match the query; hit only via the vector channel (cosine 1.0).
	topic := newTopic(4001, 1, 100, []string{"rust"})
	topic.CentroidPageRef = 9001
	writeTopic(t, engine, sparse, core.DefaultAgentID, topic)

	// With encoder: below-threshold scene is floored by the vector hit
	// (cosine 1.0 >= vectorMinScore 0.5) to threshold + cosine*0.5 = 0.55;
	// also the latest scene -> +0.1, total 0.65.
	hit, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, &mockEncoder{vec: testVec}, "unrelated", []string{"zzz"}, nil, 0.05, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	want := float32(0.05) + 1.0*vectorFloorScale + 0.1
	if hit.SceneID != 1 || !approx(hit.Score, want) {
		t.Errorf("vector hit = %+v; want SceneID=1 Score=%v (vector floor lifts below-threshold scene)", hit, want)
	}

	// No encoder: empty channel -> no hit -> empty.
	empty, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, nil, "unrelated", []string{"zzz"}, nil, 0, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if empty.SceneID != 0 || empty.Score != 0 {
		t.Errorf("expected empty hit without encoder, got %+v", empty)
	}
}

// TestTopSceneVectorChannelAgentScoped the vector channel reads centroids
// from the queried agent's own domain: a non-default agent scores the
// vector floor, and the identical query on the default domain sees nothing
// (same content hash never leaks across domains).
func TestTopSceneVectorChannelAgentScoped(t *testing.T) {
	engine := newTestEngine(t)
	const otherAgent = uint64(0x5152535455565758)
	topic := newTopic(4001, 1, 100, []string{"rust"})
	topic.CentroidPageRef = 9001
	writeTopic(t, engine, index.NewSparseIndex(), otherAgent, topic)

	// The owning agent scores the vector floor exactly like the default domain would.
	hit, err := TopScene(context.Background(), otherAgent, engine, nil, index.NewSparseIndex(), &mockEncoder{vec: testVec}, "unrelated", []string{"zzz"}, nil, 0.05, nil, nil)
	if err != nil {
		t.Fatalf("TopScene(other): %v", err)
	}
	want := float32(0.05) + 1.0*vectorFloorScale + 0.1
	if hit.SceneID != 1 || !approx(hit.Score, want) {
		t.Errorf("other-agent hit = %+v; want SceneID=1 Score=%v", hit, want)
	}

	// The default domain never sees the other agent's topic or centroid.
	empty, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, index.NewSparseIndex(), &mockEncoder{vec: testVec}, "unrelated", []string{"zzz"}, nil, 0.05, nil, nil)
	if err != nil {
		t.Fatalf("TopScene(default): %v", err)
	}
	if empty.SceneID != 0 || empty.Score != 0 {
		t.Errorf("cross-domain leak: default domain hit %+v", empty)
	}
}

// TestTopSceneThreshold threshold filter: at-or-below threshold returns empty.
func TestTopSceneThreshold(t *testing.T) {
	engine := newTestEngine(t)
	sparse := index.NewSparseIndex()
	writeTopic(t, engine, sparse, core.DefaultAgentID, newTopic(4001, 1, 100, []string{"rust"}))

	// Below threshold (score ~ 1.016) -> hit returned.
	hit, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, nil, "rust", []string{"rust"}, nil, 1.0, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if hit.SceneID != 1 {
		t.Errorf("SceneID = %d; want 1", hit.SceneID)
	}
	// Above scene score (~1.116: hit 1.0 + rrf 1/61 + recent 0.1) -> empty.
	empty, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, nil, "rust", []string{"rust"}, nil, 1.2, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if empty.SceneID != 0 || empty.Score != 0 {
		t.Errorf("expected empty hit above threshold, got %+v", empty)
	}
}

// TestTopSceneMultiActiveScenes multiple active scenes each get +0.2 (once);
// at-threshold returns empty.
func TestTopSceneMultiActiveScenes(t *testing.T) {
	engine := newTestEngine(t)
	sparse := index.NewSparseIndex()
	writeTopic(t, engine, sparse, core.DefaultAgentID, newTopic(4001, 1, 100, []string{"rust"}))
	writeTopic(t, engine, sparse, core.DefaultAgentID, newTopic(4002, 2, 200, []string{"rust"}))

	// Only scene 2 active -> scene 2 wins with exactly one +0.2.
	hit2, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, nil, "rust", []string{"rust"}, []uint64{2}, 1.1, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if hit2.SceneID != 2 || hit2.Score <= 1.2 || hit2.Score >= 1.25 {
		t.Errorf("active[2] hit = %+v; want SceneID=2 Score in (1.2, 1.25)", hit2)
	}

	// Both scenes active -> each +0.2, top score in one of them.
	hitBoth, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, nil, "rust", []string{"rust"}, []uint64{1, 2}, 1.1, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if (hitBoth.SceneID != 1 && hitBoth.SceneID != 2) || hitBoth.Score <= 1.2 || hitBoth.Score >= 1.25 {
		t.Errorf("active[1,2] hit = %+v; want SceneID in {1,2} Score in (1.2, 1.25)", hitBoth)
	}

	// Threshold above all scene scores -> empty.
	equal, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, nil, "rust", []string{"rust"}, []uint64{1, 2}, 1.25, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if equal.SceneID != 0 || equal.Score != 0 {
		t.Errorf("expected empty hit above all scene scores, got %+v", equal)
	}
}

// TestTopSceneEmpty empty db returns empty.
func TestTopSceneEmpty(t *testing.T) {
	engine := newTestEngine(t)
	sparse := index.NewSparseIndex()
	hit, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, nil, "rust", []string{"rust"}, nil, 0, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if hit.SceneID != 0 || hit.Score != 0 {
		t.Errorf("expected empty hit on empty db, got %+v", hit)
	}
}

// TestTopSceneVectorFloorNoOverride: the vector floor only lifts
// below-threshold scenes — a scene whose real signals (RRF + keyword
// overlap) already cleared the threshold keeps its score, even when another
// scene carries a perfect vector hit. Under the old formula
// (threshold + cosine) the 2.1-score vector scene would have overridden the
// 2.066 real-signal scene; the scaled fallback keeps real-signal ordering.
func TestTopSceneVectorFloorNoOverride(t *testing.T) {
	engine := newTestEngine(t)
	sparse := index.NewSparseIndex()
	// Scene 1: two keyword-matching topics (real signal ≈ 2.066), older.
	writeTopic(t, engine, sparse, core.DefaultAgentID, newTopic(4001, 1, 100, []string{"rust"}))
	writeTopic(t, engine, sparse, core.DefaultAgentID, newTopic(4002, 1, 200, []string{"rust"}))
	// Scene 2: unrelated keywords, perfect vector hit (cosine 1.0), newest.
	topic := newTopic(4003, 2, 300, []string{"unrelated"})
	topic.CentroidPageRef = 9002
	writeTopic(t, engine, sparse, core.DefaultAgentID, topic)

	hit, err := TopScene(context.Background(), core.DefaultAgentID, engine, nil, sparse, &mockEncoder{vec: testVec}, "rust", []string{"rust"}, nil, 1.0, nil, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	want := float32(2.0) + 2.0/61.0 + 2.0/62.0 // two topics: keyword hit 1.0 each + RRF ranks 1/2 per channel
	if hit.SceneID != 1 || !approx(hit.Score, want) {
		t.Errorf("hit = %+v; want SceneID=1 Score=%v (real-signal scene not overridden by vector floor)", hit, want)
	}
}

// TestCandidateTopicsSceneL3IDFilter locks the scene-domain (L3ID) filter: a
// scene's organizational L3 anchor keeps only its own scenes' topics, so a
// topic in scene B is excluded even when its content references domain A. It
// also composes with the content-semantic l3ID filter as an intersection.
func TestCandidateTopicsSceneL3IDFilter(t *testing.T) {
	engine := newTestEngine(t)
	agent := core.DefaultAgentID
	l3A := core.HashPlanNode(201, "x")
	l3B := core.HashPlanNode(202, "x")

	sa := core.NewSceneSlot("scene-a")
	sa.SceneID = 101
	sa.L3ID = l3A
	sb := core.NewSceneSlot("scene-b")
	sb.SceneID = 102
	sb.L3ID = l3B
	if err := core.WriteSceneSlot(engine, agent, sa.SceneID, &sa); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteSceneSlot(engine, agent, sb.SceneID, &sb); err != nil {
		t.Fatal(err)
	}

	// Topic 4001 belongs to scene A and references domain A; topic 4002
	// belongs to scene B but references domain A in its content L3Refs.
	t1 := newTopic(4001, 101, 100, []string{"rust"})
	t1.L3Refs = []uint64{l3A}
	writeTopic(t, engine, index.NewSparseIndex(), agent, t1)
	t2 := newTopic(4002, 102, 200, []string{"rust"})
	t2.L3Refs = []uint64{l3A}
	writeTopic(t, engine, index.NewSparseIndex(), agent, t2)

	l3AStr := common.FormatHash(l3A)
	// scene-domain only: 4002 is excluded because its scene B is anchored to
	// l3B, regardless of the l3A content ref.
	got, err := candidateTopics(agent, engine, nil, nil, &l3AStr)
	if err != nil {
		t.Fatalf("candidateTopics(sceneL3ID=A): %v", err)
	}
	if len(got) != 1 || got[0].ID != 4001 {
		t.Fatalf("sceneL3ID=A -> got %+v; want only topic 4001 (4002 belongs to scene B)", got)
	}

	// Intersection: scene-domain AND content l3ID both filter to domain A.
	both, err := candidateTopics(agent, engine, nil, &l3AStr, &l3AStr)
	if err != nil {
		t.Fatalf("candidateTopics(sceneL3ID=A, l3ID=A): %v", err)
	}
	if len(both) != 1 || both[0].ID != 4001 {
		t.Fatalf("sceneL3ID=A + l3ID=A -> got %+v; want only topic 4001", both)
	}
}
