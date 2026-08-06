// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 distillation: derive emotion state + MBTI-style personality from the
// L1 SceneNode network via LLM, then merge into the L0 ProfileSlot and
// back-fill L1 valence/arousal for downstream decay boosting.

package dream

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/common/mherrors"
	"github.com/qyiun666/MemHop/internal/common/timeutil"
	"github.com/qyiun666/MemHop/internal/repo/core/model"
	"github.com/qyiun666/MemHop/internal/repo/core/record"
	"github.com/qyiun666/MemHop/internal/repo/core/storage"
)

// maxDistillSamples caps the number of L1 SceneNodes fed into the LLM to
// keep the prompt within a stable token budget. Nodes are ranked by
// Importance × exp(-lambda * age); the top slice is kept.
const maxDistillSamples = 1000

// distillLambda is the age-decay rate (per hour) used only for sample ranking.
// It is intentionally decoupled from DecayConfig.LambdaNode so tuning the
// distill sample window never bleeds into memory decay behavior.
const distillLambda = 0.01

// L0DistillReport records the outcome of one distill run.
type L0DistillReport struct {
	SampledCount int    `json:"sampled_count"`
	TotalL1Count int    `json:"total_l1_count"`
	MBTIType     string `json:"mbti_type,omitempty"`
	L1Backfilled int    `json:"l1_backfilled"`
}

// l1Sample carries the fields needed by the LLM prompt.
type l1Sample struct {
	IDHash     uint64
	IDHex      string
	Importance float32
	Valence    float64
	Arousal    float64
	UpdatedAt  int64
	Depth      uint32
	Keywords   []string
	Summary    string
}

// emotionScore captures VAD (valence/arousal/dominance) in [0, 1].
type emotionScore struct {
	Valence   float64 `json:"valence"`
	Arousal   float64 `json:"arousal"`
	Dominance float64 `json:"dominance"`
}

// mbtiScore captures 4-dim MBTI polarity in [-1, 1] plus the derived type.
type mbtiScore struct {
	IE   float64 `json:"i_e"`
	NS   float64 `json:"n_s"`
	TF   float64 `json:"t_f"`
	JP   float64 `json:"j_p"`
	Type string  `json:"type"`
}

// distillOutput is the parsed & normalized LLM response.
type distillOutput struct {
	Emotion emotionScore
	MBTI    mbtiScore
	PerNode map[uint64]nodeEmotion
}

// nodeEmotion carries valence/arousal for one L1 SceneNode.
type nodeEmotion struct {
	Valence float64
	Arousal float64
}

// DistillL0 samples the L1 network, invokes the LLM to derive emotion + MBTI,
// then merges the results into the L0 ProfileSlot and back-fills L1 nodes.
// ChatProvider must be non-nil; the pipeline stage handles nil-chat as skip.
func DistillL0(ctx context.Context, engine *storage.StorageEngine, chat ChatProvider) (*L0DistillReport, error) {
	samples, total := sampleL1ForDistill(engine)
	report := &L0DistillReport{SampledCount: len(samples), TotalL1Count: total}
	if len(samples) == 0 {
		return report, nil
	}
	output, err := callDistillLLM(ctx, chat, samples)
	if err != nil {
		return report, err
	}
	if err := mergeIntoProfile(engine, output); err != nil {
		return report, fmt.Errorf("dream: merge distill into profile: %w", err)
	}
	report.MBTIType = output.MBTI.Type
	report.L1Backfilled = backfillL1Emotions(engine, output.PerNode)
	return report, nil
}

// sampleL1ForDistill iterates all L1 SceneNodes, ranks by Importance × recency,
// and returns the top maxDistillSamples plus the total node count.
func sampleL1ForDistill(engine *storage.StorageEngine) ([]l1Sample, int) {
	nowMs := timeutil.NowMs()
	var candidates []l1Sample
	engine.IterIndex(func(idHash, _ uint64) bool {
		node := readSceneNode(engine, idHash)
		if node == nil {
			return true
		}
		kw, summary := collectSampleContext(engine, node.TopicIDs)
		candidates = append(candidates, l1Sample{
			IDHash: idHash, IDHex: hash.FormatHash(idHash),
			Importance: node.Importance, Valence: node.Valence, Arousal: node.Arousal,
			UpdatedAt: node.UpdatedAt,
			Keywords:  kw, Summary: summary,
		})
		return true
	})
	total := len(candidates)
	sort.Slice(candidates, func(i, j int) bool {
		return rankScore(candidates[i], nowMs) > rankScore(candidates[j], nowMs)
	})
	if len(candidates) > maxDistillSamples {
		candidates = candidates[:maxDistillSamples]
	}
	return candidates, total
}

func rankScore(s l1Sample, nowMs int64) float64 {
	dtHours := float64(nowMs-s.UpdatedAt) / 3_600_000.0
	if dtHours < 0 {
		dtHours = 0
	}
	return float64(s.Importance) * math.Exp(-distillLambda*dtHours)
}

// collectSampleContext gathers fused keywords/summary from a node's L2 topics.
func collectSampleContext(engine *storage.StorageEngine, topicIDs []uint64) ([]string, string) {
	if len(topicIDs) == 0 {
		return nil, ""
	}
	var kws []string
	var parts []string
	for _, tid := range topicIDs {
		t, err := record.ReadTopicLenient(engine, tid)
		if err != nil || t == nil {
			continue
		}
		kws = append(kws, t.FusedKeywords...)
	}
	return kws, strings.Join(parts, " | ")
}

// callDistillLLM builds the prompt, sends the request, and parses the response.
func callDistillLLM(ctx context.Context, chat ChatProvider, samples []l1Sample) (*distillOutput, error) {
	if chat == nil {
		return nil, mherrors.NewError(mherrors.ErrLLM, "distill: chat provider is nil")
	}
	user := buildDistillPrompt(samples)
	response, err := chat.Chat(ctx, systemDistill, user, 4096, 0.0, 1.0)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrLLM, "distill chat call failed", err)
	}
	out, err := parseDistillResponse(response)
	if err != nil {
		return nil, mherrors.NewError(mherrors.ErrLLM, "distill parse response", err)
	}
	return out, nil
}

// mergeIntoProfile reads the current ProfileSlot, merges emotion/MBTI fields
// (never clearing other fields), then writes it back.
func mergeIntoProfile(engine *storage.StorageEngine, out *distillOutput) error {
	profileID := hash.HashID("profile")
	nowMs := timeutil.NowMs()
	slot, err := loadOrInitProfile(engine, profileID, nowMs)
	if err != nil {
		return err
	}
	applyEmotionPatch(slot, out.Emotion, nowMs)
	applyMBTIPatch(slot, out.MBTI, nowMs)
	slot.Personality = out.MBTI.Type
	return record.WriteProfileSlot(engine, profileID, slot)
}

func loadOrInitProfile(engine *storage.StorageEngine, id uint64, nowMs int64) (*model.ProfileSlot, error) {
	existing, err := record.ReadProfileSlot(engine, id)
	if err == nil && existing != nil {
		if existing.Preferences == nil {
			existing.Preferences = map[string]string{}
		}
		if existing.EmotionPatterns == nil {
			existing.EmotionPatterns = map[string]string{}
		}
		if existing.Lexicon == nil {
			existing.Lexicon = map[string]string{}
		}
		if existing.StyleTraits == nil {
			existing.StyleTraits = []string{}
		}
		return existing, nil
	}
	return &model.ProfileSlot{
		IDHash:          id,
		Name:            "Agent",
		Role:            "assistant",
		Preferences:     map[string]string{},
		Lexicon:         map[string]string{},
		StyleTraits:     []string{},
		EmotionPatterns: map[string]string{},
	}, nil
}

func applyEmotionPatch(slot *model.ProfileSlot, e emotionScore, nowMs int64) {
	slot.EmotionPatterns["valence"] = ftoa(clampUnit(e.Valence))
	slot.EmotionPatterns["arousal"] = ftoa(clampUnit(e.Arousal))
	slot.EmotionPatterns["dominance"] = ftoa(clampUnit(e.Dominance))
	slot.EmotionPatterns["updated_at_ms"] = strconv.FormatInt(nowMs, 10)
}

func applyMBTIPatch(slot *model.ProfileSlot, m mbtiScore, nowMs int64) {
	slot.Preferences["mbti_type"] = m.Type
	slot.Preferences["mbti_i_e"] = ftoa(clampSigned(m.IE))
	slot.Preferences["mbti_n_s"] = ftoa(clampSigned(m.NS))
	slot.Preferences["mbti_t_f"] = ftoa(clampSigned(m.TF))
	slot.Preferences["mbti_j_p"] = ftoa(clampSigned(m.JP))
	slot.Preferences["mbti_updated_at_ms"] = strconv.FormatInt(nowMs, 10)
}

// backfillL1Emotions writes valence/arousal into L1 SceneNodes that currently
// carry zero emotion signal. Non-zero nodes are preserved as-is.
func backfillL1Emotions(engine *storage.StorageEngine, perNode map[uint64]nodeEmotion) int {
	written := 0
	for id, em := range perNode {
		node := readSceneNode(engine, id)
		if node == nil {
			continue
		}
		if node.Valence != 0 || node.Arousal != 0 {
			continue
		}
		node.Valence = clampSigned(em.Valence)
		node.Arousal = clampUnit(em.Arousal)
		node.UpdatedAt = timeutil.NowMs()
		if err := record.WriteSceneNode(engine, id, node); err == nil {
			written++
		}
	}
	return written
}

// --- clamps + helpers ---

func clampUnit(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampSigned(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }

// deriveMBTIType regenerates the 4-letter code from clamped dimensions,
// so an inconsistent LLM "type" field never leaks through.
func deriveMBTIType(m mbtiScore) string {
	pick := func(v float64, neg, pos byte) byte {
		if v < 0 {
			return neg
		}
		return pos
	}
	return string([]byte{
		pick(m.IE, 'I', 'E'),
		pick(m.NS, 'N', 'S'),
		pick(m.TF, 'T', 'F'),
		pick(m.JP, 'J', 'P'),
	})
}

// --- prompt & parser ---

const systemDistill = `You analyze an AI agent's L1 associative memory network and derive its current emotional state and MBTI-style personality.

Input: L1 SceneNode samples (id_hex, importance, depth, keywords, summary).

Output ONLY a JSON object with this exact shape:
{
  "emotion": {"valence": 0.0..1.0, "arousal": 0.0..1.0, "dominance": 0.0..1.0},
  "mbti":    {"i_e": -1.0..1.0, "n_s": -1.0..1.0, "t_f": -1.0..1.0, "j_p": -1.0..1.0, "type": "XXXX"},
  "per_node": [{"id_hex": "16-hex-digits", "valence": 0.0..1.0, "arousal": 0.0..1.0}]
}

Rules:
- valence: 0=very negative, 1=very positive
- arousal: 0=calm, 1=highly excited
- dominance: 0=submissive, 1=dominant
- MBTI dimensions: negative = I/N/T/J, positive = E/S/F/P; magnitude = strength
- per_node only for nodes with a clear emotional signal (skip neutral ones)
- No markdown, no code fences, no commentary — JSON only`

func buildDistillPrompt(samples []l1Sample) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# L1 SceneNode samples (%d)\n\n", len(samples))
	for _, s := range samples {
		fmt.Fprintf(&b, "- id_hex=%s importance=%.3f depth=%d kw=%v\n",
			s.IDHex, s.Importance, s.Depth, s.Keywords)
		if s.Summary != "" {
			fmt.Fprintf(&b, "  summary: %s\n", s.Summary)
		}
	}
	b.WriteString("\nOutput the JSON now.")
	return b.String()
}

func parseDistillResponse(response string) (*distillOutput, error) {
	cleaned := stripCodeBlocks(response)
	var raw struct {
		Emotion emotionScore `json:"emotion"`
		MBTI    mbtiScore    `json:"mbti"`
		PerNode []struct {
			IDHex   string  `json:"id_hex"`
			Valence float64 `json:"valence"`
			Arousal float64 `json:"arousal"`
		} `json:"per_node"`
	}
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, fmt.Errorf("unmarshal distill JSON: %w", err)
	}
	// Normalize MBTI: clamp dims, regenerate type from dims to guarantee consistency.
	raw.MBTI.IE = clampSigned(raw.MBTI.IE)
	raw.MBTI.NS = clampSigned(raw.MBTI.NS)
	raw.MBTI.TF = clampSigned(raw.MBTI.TF)
	raw.MBTI.JP = clampSigned(raw.MBTI.JP)
	raw.MBTI.Type = deriveMBTIType(raw.MBTI)
	out := &distillOutput{
		Emotion: raw.Emotion, MBTI: raw.MBTI,
		PerNode: make(map[uint64]nodeEmotion, len(raw.PerNode)),
	}
	for _, p := range raw.PerNode {
		id, err := hash.ParseID(p.IDHex)
		if err != nil {
			continue
		}
		out.PerNode[id] = nodeEmotion{Valence: p.Valence, Arousal: p.Arousal}
	}
	return out, nil
}
