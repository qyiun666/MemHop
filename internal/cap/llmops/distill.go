// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Distill is the L1→L0 distillation call point — derives the agent's
// emotional state and MBTI-style profile from L1 associative samples.

package llmops

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// distillMaxTokens bounds one distill call's output; reasoning models can
// exhaust it mid-JSON, hence the escalation the transport applies.
const distillMaxTokens = 2048

// distillPersonalityMaxRunes caps the distilled personality summary so the
// L0 digest stays compact.
const distillPersonalityMaxRunes = 160

// L1Sample is a distill input assembled from an L1 node and its topics (keywords
// come from linked L2 topics); the prompt reads every field it carries.
type L1Sample = core.DistillSample

type EmotionScore = core.EmotionScore

type MBTIScore = core.MBTIScore

type DistillOutput struct {
	Emotion     EmotionScore
	MBTI        MBTIScore
	Personality string
	// PerNode is keyed by node id — the parser already resolved which row is whose.
	PerNode map[uint64]core.NodeEmotion
}

// systemDistill states the reply contract. The personality budget it quotes is the
// one the parser enforces, so the model never writes to a length the reply is then
// cut at.
var systemDistill = fmt.Sprintf(`You analyze an AI agent's L1 associative memory samples and derive its current emotional state, MBTI-style personality dimensions, and a short personality summary.

Output ONLY a JSON object:
{
  "emotion": {"valence": 0.0..1.0, "arousal": 0.0..1.0, "dominance": 0.0..1.0},
  "mbti": {"i_e": -1.0..1.0, "n_s": -1.0..1.0, "t_f": -1.0..1.0, "j_p": -1.0..1.0},
  "personality": "<short third-person trait summary>",
  "per_node": [{"id_hex": "16-hex-digits", "valence": 0.0..1.0, "arousal": 0.0..1.0}]
}

Rules:
- valence: 0=very negative, 0.5=neutral, 1=very positive
- arousal: 0=calm, 1=highly excited
- dominance: 0=submissive, 1=dominant
- MBTI dimensions: negative = I/N/T/J, positive = E/S/F/P; magnitude = strength, so answer 0 only when the samples truly say nothing about that axis
- personality: at most %d characters, third person; describe the durable character traits these samples reveal — base it strictly on the samples, never invent
- per_node: at most 20 rows, only the nodes with the strongest emotional signal (skip neutral ones)
- No markdown, no code fences, no commentary — JSON only`, distillPersonalityMaxRunes)

// distillFormatRetry is appended to the user prompt for the format-constrained retry.
const distillFormatRetry = `

Output ONLY valid JSON per the schema. No markdown, no code fences, no commentary.`

// Distill derives emotional state, MBTI dimensions and a personality
// summary from L1 node samples for L0 profile merging.
func Distill(ctx context.Context, chat Chat, samples []L1Sample) (*DistillOutput, error) {
	if len(samples) == 0 {
		return nil, common.NewError(common.ErrLLM, "distill: no samples")
	}
	// The ids this prompt offered; see parseDistillResponse for what happens to a
	// row naming anything else.
	known := make(map[uint64]struct{}, len(samples))
	for _, s := range samples {
		known[s.IDHash] = struct{}{}
	}
	return askJSON(ctx, chat, jsonExchange{
		what:        "distill",
		system:      systemDistill,
		user:        buildDistillPrompt(samples),
		formatRetry: distillFormatRetry,
		ceiling:     distillMaxTokens,
	}, func(reply string) (*DistillOutput, error) { return parseDistillResponse(reply, known) })
}

func buildDistillPrompt(samples []L1Sample) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# L1 samples (%d)\n\n", len(samples))
	for _, s := range samples {
		fmt.Fprintf(&b, "- id_hex=%s importance=%.3f kw=%v\n",
			common.FormatHash(s.IDHash), s.Importance, s.Keywords)
	}
	b.WriteString("\nOutput the JSON now.")
	return b.String()
}

// parseDistillResponse reads one reply against the contract. known is the id set
// this pass put in front of the model: a row naming anything else is dropped, since
// only a sampled node has somewhere to backfill into.
func parseDistillResponse(response string, known map[uint64]struct{}) (*DistillOutput, error) {
	cleaned := stripCodeBlocks(response)
	var raw struct {
		Emotion *EmotionScore `json:"emotion"`
		MBTI    *struct {
			IE float64 `json:"i_e"`
			NS float64 `json:"n_s"`
			TF float64 `json:"t_f"`
			JP float64 `json:"j_p"`
		} `json:"mbti"`
		Personality string `json:"personality"`
		PerNode     []struct {
			IDHex   string  `json:"id_hex"`
			Valence float64 `json:"valence"`
			Arousal float64 `json:"arousal"`
		} `json:"per_node"`
	}
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, common.NewError(common.ErrLLM, "distill response parse failed", err)
	}
	// Valid JSON that answers none of the contract is no answer: its zeros would
	// erase the distilled emotion and derive a type word from four silent dimensions.
	if raw.Emotion == nil || raw.MBTI == nil {
		return nil, common.NewError(common.ErrLLM, "distill response carries no emotion or mbti block")
	}
	personality := strings.TrimSpace(raw.Personality)
	if r := []rune(personality); len(r) > distillPersonalityMaxRunes {
		personality = string(r[:distillPersonalityMaxRunes])
	}
	out := &DistillOutput{
		Emotion: EmotionScore{
			Valence:   clampUnit(raw.Emotion.Valence),
			Arousal:   clampUnit(raw.Emotion.Arousal),
			Dominance: clampUnit(raw.Emotion.Dominance),
		},
		MBTI: MBTIScore{
			IE: clampSigned(raw.MBTI.IE),
			NS: clampSigned(raw.MBTI.NS),
			TF: clampSigned(raw.MBTI.TF),
			JP: clampSigned(raw.MBTI.JP),
		},
		Personality: personality,
		PerNode:     make(map[uint64]core.NodeEmotion, len(raw.PerNode)),
	}
	// Type re-derived from the four dimensions (never trusted from the LLM).
	out.MBTI.Type = core.DeriveMBTIType(out.MBTI)
	for _, n := range raw.PerNode {
		id, err := common.ParseID(n.IDHex)
		if err != nil {
			continue // skip rows with unparsable ids
		}
		if _, ok := known[id]; !ok {
			continue // a node this pass never sampled has nothing to backfill into it
		}
		out.PerNode[id] = core.NodeEmotion{
			Valence: clampUnit(n.Valence), Arousal: clampUnit(n.Arousal),
		}
	}
	return out, nil
}

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
