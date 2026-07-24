// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"errors"
	"testing"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/core/model"
	"github.com/qyiun666/MemHop/internal/core/storage"
)

// mockChatProvider drives DistillL0 without hitting a real LLM.
type mockChatProvider struct {
	Response string
	Err      error
}

func (m *mockChatProvider) Chat(_, _ string, _ int, _, _ float32) (string, error) {
	if m.Err != nil {
		return "", m.Err
	}
	return m.Response, nil
}

// distillTestFixture creates an engine with N L1 SceneNodes ready for distill.
func distillTestFixture(t *testing.T, n int) (*storage.StorageEngine, []uint64) {
	t.Helper()
	engine := createTestEngine(t)
	ids := make([]uint64, n)
	for i := 0; i < n; i++ {
		id := hash.HashID("l1_distill_" + string(rune('a'+i)))
		ids[i] = id
		node := &model.SceneNode{
			IDHash: id, SceneID: uint64(1000 + i),
			TopicIDs: []uint64{}, Depth: 1,
			Importance: 0.8, Valence: 0, Arousal: 0,
			CreatedAt: 100, UpdatedAt: 100,
		}
		writeTestSceneNode(t, engine, node)
	}
	return engine, ids
}

// happyPathResponse is a compliant LLM output for a 2-node fixture.
const happyPathResponse = `{
  "emotion": {"valence": 0.7, "arousal": 0.4, "dominance": 0.6},
  "mbti": {"i_e": -0.6, "n_s": -0.4, "t_f": 0.3, "j_p": -0.2, "type": "IGNORED"},
  "per_node": [
    {"id_hex": "%s", "valence": 0.5, "arousal": 0.3}
  ]
}`

func TestDistillL0_HappyPath(t *testing.T) {
	engine, ids := distillTestFixture(t, 2)
	chat := &mockChatProvider{
		Response: formatResponse(happyPathResponse, hash.FormatHash(ids[0])),
	}

	report, err := DistillL0(engine, chat)
	if err != nil {
		t.Fatalf("DistillL0 err: %v", err)
	}
	if report.SampledCount != 2 || report.TotalL1Count != 2 {
		t.Errorf("Sampled=%d Total=%d; want 2/2", report.SampledCount, report.TotalL1Count)
	}
	// Type must be derived from clamped dims: (-, -, +, -) => "INFJ"
	if report.MBTIType != "INFJ" {
		t.Errorf("MBTIType=%q; want INFJ (derived from dims)", report.MBTIType)
	}
	profile := readTestProfile(t, engine, hash.HashID("profile"))
	if profile.Personality != "INFJ" {
		t.Errorf("Personality=%q; want INFJ", profile.Personality)
	}
	if got := profile.EmotionPatterns["valence"]; got != "0.700" {
		t.Errorf("valence=%q; want 0.700", got)
	}
	if profile.Preferences["mbti_type"] != "INFJ" {
		t.Errorf("Preferences.mbti_type=%q", profile.Preferences["mbti_type"])
	}
	// L1 backfill: only id[0] was in per_node — check it got written.
	if report.L1Backfilled != 1 {
		t.Errorf("L1Backfilled=%d; want 1", report.L1Backfilled)
	}
	node := readTestSceneNode(t, engine, ids[0])
	if node.Valence == 0 || node.Arousal == 0 {
		t.Errorf("node[0] Valence=%v Arousal=%v; want non-zero", node.Valence, node.Arousal)
	}
}

func TestDistillL0_PreservesOtherProfileFields(t *testing.T) {
	engine, _ := distillTestFixture(t, 1)
	// Seed a profile with pre-existing fields that must survive distill.
	profileID := hash.HashID("profile")
	seed := &model.ProfileSlot{
		IDHash: profileID, Name: "Alice", Role: "assistant",
		Worldview: "curious about the world",
		Preferences: map[string]string{
			"top_keywords":            "cats, tea",
			"personality.temperature": "0.7",
		},
		Lexicon:         map[string]string{"docker": "container tool"},
		StyleTraits:     []string{"concise"},
		EmotionPatterns: map[string]string{"legacy_marker": "keep_me"},
		CreatedAt:       1, UpdatedAt: 1, Version: 3,
	}
	writeTestProfile(t, engine, seed)

	chat := &mockChatProvider{Response: `{"emotion":{"valence":0.5,"arousal":0.5,"dominance":0.5},"mbti":{"i_e":0.1,"n_s":0.1,"t_f":0.1,"j_p":0.1,"type":"ESFP"},"per_node":[]}`}
	if _, err := DistillL0(engine, chat); err != nil {
		t.Fatalf("DistillL0 err: %v", err)
	}
	p := readTestProfile(t, engine, profileID)
	if p.Name != "Alice" || p.Role != "assistant" || p.Worldview != "curious about the world" {
		t.Errorf("core identity fields were overwritten: name=%q role=%q worldview=%q",
			p.Name, p.Role, p.Worldview)
	}
	if p.Preferences["top_keywords"] != "cats, tea" {
		t.Errorf("top_keywords lost: %q", p.Preferences["top_keywords"])
	}
	if p.Preferences["personality.temperature"] != "0.7" {
		t.Errorf("agent-side personality.temperature lost: %q", p.Preferences["personality.temperature"])
	}
	if p.Lexicon["docker"] != "container tool" {
		t.Errorf("Lexicon lost: %v", p.Lexicon)
	}
	if len(p.StyleTraits) != 1 || p.StyleTraits[0] != "concise" {
		t.Errorf("StyleTraits lost: %v", p.StyleTraits)
	}
	if p.EmotionPatterns["legacy_marker"] != "keep_me" {
		t.Errorf("legacy EmotionPatterns key lost: %v", p.EmotionPatterns)
	}
	// New fields must be present too.
	if p.Preferences["mbti_type"] != "ESFP" {
		t.Errorf("mbti_type not written: %q", p.Preferences["mbti_type"])
	}
}

func TestDistillL0_BackfillSkipsNonZeroNodes(t *testing.T) {
	engine, ids := distillTestFixture(t, 1)
	// Manually pre-set Valence on the node so backfill must NOT overwrite.
	preset := readTestSceneNode(t, engine, ids[0])
	preset.Valence = 0.9
	preset.Arousal = 0.8
	writeTestSceneNode(t, engine, preset)

	chat := &mockChatProvider{
		Response: formatResponse(happyPathResponse, hash.FormatHash(ids[0])),
	}
	report, err := DistillL0(engine, chat)
	if err != nil {
		t.Fatalf("DistillL0 err: %v", err)
	}
	if report.L1Backfilled != 0 {
		t.Errorf("L1Backfilled=%d; want 0 (non-zero nodes must be preserved)", report.L1Backfilled)
	}
	node := readTestSceneNode(t, engine, ids[0])
	if node.Valence != 0.9 || node.Arousal != 0.8 {
		t.Errorf("preset values overwritten: v=%v a=%v", node.Valence, node.Arousal)
	}
}

func TestDistillL0_EmptyL1(t *testing.T) {
	engine := createTestEngine(t)
	chat := &mockChatProvider{Response: `{"should":"not be called"}`}
	report, err := DistillL0(engine, chat)
	if err != nil {
		t.Fatalf("DistillL0 err on empty L1: %v", err)
	}
	if report.SampledCount != 0 || report.TotalL1Count != 0 {
		t.Errorf("empty L1 counts: Sampled=%d Total=%d", report.SampledCount, report.TotalL1Count)
	}
	// LLM must NOT be called and profile must NOT be created.
	if report.MBTIType != "" {
		t.Errorf("MBTIType should be empty on skip, got %q", report.MBTIType)
	}
}

func TestDistillL0_LLMError(t *testing.T) {
	engine, _ := distillTestFixture(t, 1)
	chat := &mockChatProvider{Err: errors.New("network timeout")}
	if _, err := DistillL0(engine, chat); err == nil {
		t.Error("expected error when LLM fails, got nil")
	}
}

func TestDistillL0_MalformedJSON(t *testing.T) {
	engine, _ := distillTestFixture(t, 1)
	chat := &mockChatProvider{Response: "not a json"}
	if _, err := DistillL0(engine, chat); err == nil {
		t.Error("expected error on malformed JSON, got nil")
	}
}

func TestDistillL0_MBTIClamp(t *testing.T) {
	engine, ids := distillTestFixture(t, 1)
	// Feed out-of-range dims plus an inconsistent type field.
	body := `{"emotion":{"valence":2.5,"arousal":-0.4,"dominance":0.5},"mbti":{"i_e":2.5,"n_s":-3.0,"t_f":0.0,"j_p":-0.5,"type":"WRONG"},"per_node":[]}`
	chat := &mockChatProvider{Response: body}
	report, err := DistillL0(engine, chat)
	if err != nil {
		t.Fatalf("DistillL0 err: %v", err)
	}
	// Emotion values clamped to unit range in Preferences write.
	p := readTestProfile(t, engine, hash.HashID("profile"))
	if p.EmotionPatterns["valence"] != "1.000" {
		t.Errorf("valence clamp: got %q, want 1.000", p.EmotionPatterns["valence"])
	}
	if p.EmotionPatterns["arousal"] != "0.000" {
		t.Errorf("arousal clamp: got %q, want 0.000", p.EmotionPatterns["arousal"])
	}
	// MBTI dims clamped to [-1, 1]; type derived from clamped dims.
	if p.Preferences["mbti_i_e"] != "1.000" {
		t.Errorf("i_e clamp: got %q, want 1.000", p.Preferences["mbti_i_e"])
	}
	if p.Preferences["mbti_n_s"] != "-1.000" {
		t.Errorf("n_s clamp: got %q, want -1.000", p.Preferences["mbti_n_s"])
	}
	// (E, N, F, J) after clamp: i_e>0=E, n_s<0=N, t_f=0.0 => F (default pos), j_p<0=J
	if report.MBTIType != "ENFJ" {
		t.Errorf("MBTIType=%q; want ENFJ (derived from clamped dims)", report.MBTIType)
	}
	// Sanity: id[0] valence remained 0 (per_node was empty).
	node := readTestSceneNode(t, engine, ids[0])
	if node.Valence != 0 || node.Arousal != 0 {
		t.Errorf("L1 node unexpectedly modified: v=%v a=%v", node.Valence, node.Arousal)
	}
}

func TestDistillL0_NilChatSkips(t *testing.T) {
	engine, _ := distillTestFixture(t, 1)
	if _, err := DistillL0(engine, nil); err == nil {
		t.Error("expected error when chat is nil, got nil")
	}
}

// formatResponse fills the id_hex placeholder in a template.
func formatResponse(template, idHex string) string {
	out := make([]byte, 0, len(template)+16)
	i := 0
	for i < len(template) {
		if i+1 < len(template) && template[i] == '%' && template[i+1] == 's' {
			out = append(out, idHex...)
			i += 2
			continue
		}
		out = append(out, template[i])
		i++
	}
	return string(out)
}
