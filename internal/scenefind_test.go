// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// mockEncoder returns a fixed vector, simulating an available encoder.
type mockEncoder struct {
	vec []float32
}

func (m *mockEncoder) Encode(string) ([]float32, error) { return m.vec, nil }
func (m *mockEncoder) Dim() int                         { return len(m.vec) }
func (m *mockEncoder) Mode() string                     { return "mock" }
func (m *mockEncoder) IsAvailable() bool                { return true }

// testVec is shared with topic vector pages: identical 768-dim vector, cosine 1.0.
var testVec = func() []float32 {
	v := make([]float32, 768)
	for i := range v {
		v[i] = 0.5
	}
	return v
}()

func TestMain(m *testing.M) {
	if err := index.InitTokenizer(index.EngineAuto); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func newTestEngine(t *testing.T) *core.StorageEngine {
	t.Helper()
	engine, err := core.Create(filepath.Join(t.TempDir(), "test.meh"), 768)
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { engine.Close(&core.IndexSnapshotData{}) })
	return engine
}

func newTopic(id, scene uint64, ts int64, kws []string) core.TopicSlot {
	return core.TopicSlot{
		ID:            id,
		SceneID:       scene,
		Depth:         1,
		UserKeywords:  kws,
		UserTimestamp: ts,
	}
}

// writeTopic writes a topic record + sparse index; writes the fixed vector page when CentroidPageRef != 0.
func writeTopic(t *testing.T, engine *core.StorageEngine, sparse *index.SparseIndex, topic core.TopicSlot) {
	t.Helper()
	data, err := json.Marshal(topic)
	if err != nil {
		t.Fatalf("marshal topic: %v", err)
	}
	if _, err := engine.WriteRecord(core.RecL2Topic, topic.ID, data); err != nil {
		t.Fatalf("write topic: %v", err)
	}
	fields := make([]string, 0, len(topic.FusedKeywords)+len(topic.UserKeywords)+len(topic.AgentKeywords))
	fields = append(fields, topic.FusedKeywords...)
	fields = append(fields, topic.UserKeywords...)
	fields = append(fields, topic.AgentKeywords...)
	terms := index.Tokenize(strings.Join(fields, " "))
	sparse.AddDocument(topic.ID, terms, uint32(len(terms)))
	if topic.CentroidPageRef != 0 {
		if _, err := engine.WriteRecord(core.RecVecCentroid, topic.CentroidPageRef, common.F32SliceToBytes(testVec)); err != nil {
			t.Fatalf("write vector: %v", err)
		}
	}
}

func approx(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-4 }

// TestKeywordHit verifies the union of the three []string fields, dedup and hit ratio.
func TestKeywordHit(t *testing.T) {
	topic := core.TopicSlot{
		FusedKeywords: []string{"rust"},
		UserKeywords:  []string{"rust", "tokio"},
		AgentKeywords: []string{"Tokio", "async"},
	}
	// Union of 3 fields (lowercased, dedup): rust, tokio, async; query rust, async, cpp -> 2/3 hits.
	got := keywordHit(topic, keywordSet([]string{"rust", "async", "cpp"}))
	if want := float32(2) / 3; !approx(got, want) {
		t.Errorf("keywordHit = %v; want %v", got, want)
	}
	// Empty query keywords -> 0.
	if got := keywordHit(topic, keywordSet(nil)); got != 0 {
		t.Errorf("keywordHit(empty) = %v; want 0", got)
	}
	// Query dedup: repeated keywords count once in the denominator.
	if got := keywordHit(topic, keywordSet([]string{"rust", "rust"})); !approx(got, 1.0) {
		t.Errorf("keywordHit(dup) = %v; want 1.0", got)
	}
}

// TestApplySceneBonuses verifies scene bonuses: 0.2/0.1 each once, active takes priority.
func TestApplySceneBonuses(t *testing.T) {
	// Active scene +0.2 (once).
	scores := map[uint64]float32{1: 1.0}
	applySceneBonuses(scores, map[uint64]struct{}{1: {}}, 1, DefaultMemHopDefaults)
	if !approx(scores[1], 1.2) {
		t.Errorf("active bonus: scores[1] = %v; want 1.2", scores[1])
	}

	// Latest-timestamp scene +0.1 (when not active).
	scores = map[uint64]float32{2: 1.0}
	applySceneBonuses(scores, map[uint64]struct{}{}, 2, DefaultMemHopDefaults)
	if !approx(scores[2], 1.1) {
		t.Errorf("recent bonus: scores[2] = %v; want 1.1", scores[2])
	}

	// Mutual exclusion: active + latest -> only +0.2.
	scores = map[uint64]float32{3: 1.0}
	applySceneBonuses(scores, map[uint64]struct{}{3: {}}, 3, DefaultMemHopDefaults)
	if !approx(scores[3], 1.2) {
		t.Errorf("mutual exclusion: scores[3] = %v; want 1.2", scores[3])
	}

	// Latest scene without a score -> no bonus.
	scores = map[uint64]float32{4: 1.0}
	applySceneBonuses(scores, map[uint64]struct{}{}, 5, DefaultMemHopDefaults)
	if !approx(scores[4], 1.0) {
		t.Errorf("no bonus for missing scene: scores[4] = %v; want 1.0", scores[4])
	}
}

// TestTopSceneRelevanceOrder: the hit scene's Topics must be ordered by
// fused relevance, not recency — an older matching topic ranks above a
// newer non-matching one.
func TestTopSceneRelevanceOrder(t *testing.T) {
	engine := newTestEngine(t)
	sparse := index.NewSparseIndex()
	// scene 1: old topic about rust (t=100), new topic about cooking (t=300).
	writeTopic(t, engine, sparse, newTopic(4001, 1, 100, []string{"rust"}))
	writeTopic(t, engine, sparse, newTopic(4002, 1, 300, []string{"cooking"}))

	hit, err := TopScene(context.Background(), engine, nil, sparse, nil, "rust", []string{"rust"}, nil, DefaultMemHopDefaults, 0, nil)
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
	writeTopic(t, engine, sparse, newTopic(4001, 1, 100, []string{"rust"}))
	writeTopic(t, engine, sparse, newTopic(4002, 2, 200, []string{"cooking"}))

	hit, err := TopScene(context.Background(), engine, nil, sparse, nil, "rust", []string{"rust"}, nil, DefaultMemHopDefaults, 0, nil)
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
	writeTopic(t, engine, sparse, newTopic(4001, 1, 100, []string{"rust"}))
	writeTopic(t, engine, sparse, newTopic(4002, 1, 200, []string{"rust"}))
	writeTopic(t, engine, sparse, newTopic(4003, 2, 300, []string{"cooking"}))

	hit, err := TopScene(context.Background(), engine, nil, sparse, nil, "rust", []string{"rust"}, nil, DefaultMemHopDefaults, 0, nil)
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
	writeTopic(t, engine, sparse, newTopic(4001, 1, 100, []string{"rust"}))

	want := float32(1.0) + 2.0/61.0 + 0.2
	hit, err := TopScene(context.Background(), engine, nil, sparse, nil, "rust", []string{"rust"}, []uint64{1, 1}, DefaultMemHopDefaults, 1.15, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if hit.SceneID != 1 || !approx(hit.Score, want) {
		t.Errorf("hit = %+v; want SceneID=1 Score=%v (bonus added once)", hit, want)
	}

	// No active scene -> no 0.2, below the 1.15 threshold -> empty.
	empty, err := TopScene(context.Background(), engine, nil, sparse, nil, "rust", []string{"rust"}, nil, DefaultMemHopDefaults, 1.15, nil)
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
	writeTopic(t, engine, sparse, newTopic(4001, 1, 100, []string{"rust"}))

	// Latest-topic scene +0.1: score = 1.0 + 2/61 (bm25+entity) + 0.1.
	recent, err := TopScene(context.Background(), engine, nil, sparse, nil, "rust", []string{"rust"}, nil, DefaultMemHopDefaults, 1.05, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	wantRecent := float32(1.0) + 2.0/61.0 + 0.1
	if recent.SceneID != 1 || !approx(recent.Score, wantRecent) {
		t.Errorf("recent hit = %+v; want Score=%v", recent, wantRecent)
	}

	// Same scene active -> active priority, only +0.2 not +0.1.
	active, err := TopScene(context.Background(), engine, nil, sparse, nil, "rust", []string{"rust"}, []uint64{1}, DefaultMemHopDefaults, 1.15, nil)
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
	writeTopic(t, engine, sparse, topic)

	// With encoder: vector hit (cosine 1.0 >= VectorMinScore 0.5) floors the scene
	// to threshold+1.0=1.05; also the latest scene -> +0.1, total 1.15.
	hit, err := TopScene(context.Background(), engine, nil, sparse, &mockEncoder{vec: testVec}, "unrelated", []string{"zzz"}, nil, DefaultMemHopDefaults, 0.05, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	want := float32(0.05) + 1.0 + 0.1
	if hit.SceneID != 1 || !approx(hit.Score, want) {
		t.Errorf("vector hit = %+v; want SceneID=1 Score=%v (vector floor)", hit, want)
	}

	// No encoder: empty channel -> no hit -> empty.
	empty, err := TopScene(context.Background(), engine, nil, sparse, nil, "unrelated", []string{"zzz"}, nil, DefaultMemHopDefaults, 0, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if empty.SceneID != 0 || empty.Score != 0 {
		t.Errorf("expected empty hit without encoder, got %+v", empty)
	}
}

// TestTopSceneThreshold threshold filter: at-or-below threshold returns empty.
func TestTopSceneThreshold(t *testing.T) {
	engine := newTestEngine(t)
	sparse := index.NewSparseIndex()
	writeTopic(t, engine, sparse, newTopic(4001, 1, 100, []string{"rust"}))

	// Below threshold (score ~ 1.016) -> hit returned.
	hit, err := TopScene(context.Background(), engine, nil, sparse, nil, "rust", []string{"rust"}, nil, DefaultMemHopDefaults, 1.0, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if hit.SceneID != 1 {
		t.Errorf("SceneID = %d; want 1", hit.SceneID)
	}
	// Above scene score (~1.116: hit 1.0 + rrf 1/61 + recent 0.1) -> empty.
	empty, err := TopScene(context.Background(), engine, nil, sparse, nil, "rust", []string{"rust"}, nil, DefaultMemHopDefaults, 1.2, nil)
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
	writeTopic(t, engine, sparse, newTopic(4001, 1, 100, []string{"rust"}))
	writeTopic(t, engine, sparse, newTopic(4002, 2, 200, []string{"rust"}))

	// Only scene 2 active -> scene 2 wins with exactly one +0.2.
	hit2, err := TopScene(context.Background(), engine, nil, sparse, nil, "rust", []string{"rust"}, []uint64{2}, DefaultMemHopDefaults, 1.1, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if hit2.SceneID != 2 || hit2.Score <= 1.2 || hit2.Score >= 1.25 {
		t.Errorf("active[2] hit = %+v; want SceneID=2 Score in (1.2, 1.25)", hit2)
	}

	// Both scenes active -> each +0.2, top score in one of them.
	hitBoth, err := TopScene(context.Background(), engine, nil, sparse, nil, "rust", []string{"rust"}, []uint64{1, 2}, DefaultMemHopDefaults, 1.1, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if (hitBoth.SceneID != 1 && hitBoth.SceneID != 2) || hitBoth.Score <= 1.2 || hitBoth.Score >= 1.25 {
		t.Errorf("active[1,2] hit = %+v; want SceneID in {1,2} Score in (1.2, 1.25)", hitBoth)
	}

	// Threshold above all scene scores -> empty.
	equal, err := TopScene(context.Background(), engine, nil, sparse, nil, "rust", []string{"rust"}, []uint64{1, 2}, DefaultMemHopDefaults, 1.25, nil)
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
	hit, err := TopScene(context.Background(), engine, nil, sparse, nil, "rust", []string{"rust"}, nil, DefaultMemHopDefaults, 0, nil)
	if err != nil {
		t.Fatalf("TopScene: %v", err)
	}
	if hit.SceneID != 0 || hit.Score != 0 {
		t.Errorf("expected empty hit on empty db, got %+v", hit)
	}
}

// TestActivateSceneDedup activation dedup: repeats keep first-order positions.
func TestActivateSceneDedup(t *testing.T) {
	cfg := *DefaultMemHopDefaults
	cfg.Capacity = 7
	db := &DB{config: &MemHopConfig{Defaults: cfg}}
	db.activateScene(7)
	db.activateScene(7)
	db.activateScene(9)
	if len(db.activeScenes) != 2 {
		t.Fatalf("len(activeScenes) = %d; want 2", len(db.activeScenes))
	}
	if db.activeScenes[0] != 7 || db.activeScenes[1] != 9 {
		t.Errorf("activeScenes = %v; want [7 9]", db.activeScenes)
	}
}

// TestActivateSceneUnbounded verifies the active set grows past Capacity
// without eviction: Dream size is controlled by Update, which triggers a
// Dream on the oldest scene at Defaults.Capacity.
func TestActivateSceneUnbounded(t *testing.T) {
	cfg := *DefaultMemHopDefaults
	cfg.Capacity = 2
	db := &DB{config: &MemHopConfig{Defaults: cfg}}
	db.activateScene(7)
	db.activateScene(9)
	db.activateScene(11)
	if len(db.activeScenes) != 3 || db.activeScenes[0] != 7 || db.activeScenes[1] != 9 || db.activeScenes[2] != 11 {
		t.Fatalf("activeScenes = %v; want [7 9 11]", db.activeScenes)
	}
}

// TestSpreadingActivation covers one-hop and two-hop activation, start-scene
// exclusion, threshold pruning and the no-node/isolated empty results.
func TestSpreadingActivation(t *testing.T) {
	engine := newTestEngine(t)
	sceneA := common.FormatHash(common.HashID("sceneA"))
	sceneB := common.FormatHash(common.HashID("sceneB"))
	sceneC := common.FormatHash(common.HashID("sceneC"))
	sceneD := common.FormatHash(common.HashID("sceneD")) // isolated: node, no edges

	mk := func(scene string, kws []string) {
		t.Helper()
		if !repo.CreateTopicL2(engine, scene, kws, 1000, 0) {
			t.Fatal("create topic")
		}
	}
	mk(sceneA, []string{"memory", "agent"})
	mk(sceneB, []string{"memory", "database"})
	mk(sceneC, []string{"database", "code"})
	mk(sceneD, []string{"cooking", "food"})
	if _, err := repo.SyncL1NodesFromL2(engine); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := repo.BuildL1Hyperedges(engine, 0.15); err != nil {
		t.Fatalf("build edges: %v", err)
	}
	defaults := &MemHopDefaults{
		L1EdgeMaxHops:         2,
		L1ActivationDampening: 0.5,
		L1ActivationThreshold: 0.05,
		L1AssocMaxScenes:      3,
	}
	l2Meta := index.BuildL2MetaFromEngine(engine)
	sceneBHash := mustParse(t, sceneB)
	sceneCHash := mustParse(t, sceneC)
	sceneDHash := mustParse(t, sceneD)

	// A-B share "memory" (J=1/3): activation = 1×1/3×0.5 ≈ 0.1667. The
	// two-hop B→C path yields 0.1667×1/3×0.5 ≈ 0.0278 < 0.05 → pruned.
	hits := SpreadingActivation(engine, l2Meta, mustParse(t, sceneA), defaults)
	if len(hits) != 1 || hits[0].SceneID != sceneBHash {
		t.Fatalf("want only scene B, got %+v", hits)
	}
	if math.Abs(float64(hits[0].Score)-1.0/6.0) > 1e-4 {
		t.Fatalf("activation = %.4f, want 0.1667", hits[0].Score)
	}
	if len(hits[0].Topics) != 1 {
		t.Fatalf("want 1 topic on B, got %d", len(hits[0].Topics))
	}

	// Lower threshold lets the two-hop path through: C joins after B.
	defaults.L1ActivationThreshold = 0.01
	hits = SpreadingActivation(engine, l2Meta, mustParse(t, sceneA), defaults)
	if len(hits) != 2 || hits[0].SceneID != sceneBHash || hits[1].SceneID != sceneCHash {
		t.Fatalf("want B then C, got %+v", hits)
	}
	if hits[0].Score < hits[1].Score {
		t.Fatal("B must activate stronger than C")
	}

	// A scene without an L1 node (created after the last Dream) → empty.
	sceneFresh := common.FormatHash(common.HashID("sceneFresh"))
	if hits := SpreadingActivation(engine, l2Meta, mustParse(t, sceneFresh), defaults); len(hits) != 0 {
		t.Fatalf("fresh scene must have no associations, got %+v", hits)
	}

	// An isolated scene has a node but no edges → empty.
	if hits := SpreadingActivation(engine, l2Meta, sceneDHash, defaults); len(hits) != 0 {
		t.Fatalf("isolated scene must have no associations, got %+v", hits)
	}
}

func mustParse(t *testing.T, s string) uint64 {
	t.Helper()
	v, err := common.ParseID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}
