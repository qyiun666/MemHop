// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Profile generation and distillation policy (moved out of the record layer
// so the repository keeps record reads/writes only): the L0 digest is a
// projection of the keyword distribution plus the distilled emotion/MBTI
// signals of the L1 network.

package profile

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// maxDistillSamples bounds both prompt cost and LLM input size for L0
// distillation. 200 top-ranked nodes is far more signal than emotion/MBTI
// extraction needs.
const maxDistillSamples = 200

// maxDistillKeywordsPerSample bounds the keyword list sent for each node.
const maxDistillKeywordsPerSample = 20

// distillSampleLambda: sample-rank age decay per hour (ranking only,
// decoupled from the LambdaNode decay config).
const distillSampleLambda = 0.01

// Generate rebuilds the L0 profile from the sparse keyword distribution:
// creates a default profile when absent, else refreshes personality and the
// keyword/memory preference fields.
func Generate(engine *core.StorageEngine, agentID uint64, sparse *index.SparseIndex) error {
	topKeywords := sparse.TopTerms(20)
	topTerms := make([]string, len(topKeywords))
	for i, tk := range topKeywords {
		topTerms[i] = tk.Term
	}
	totalEngrams := engine.AgentRecordCount(agentID)
	slot, err := repo.GetProfileL0(engine, agentID)
	if err != nil {
		return repo.UpdateProfileL0(engine, agentID, Default(topTerms, totalEngrams))
	}
	slot.Personality = joinTopTerms(topTerms, 5)
	if slot.Preferences == nil {
		slot.Preferences = map[string]string{}
	}
	slot.Preferences["top_keywords"] = joinTopTerms(topTerms, 20)
	slot.Preferences["total_engrams"] = fmt.Sprintf("%d", totalEngrams)
	return repo.UpdateProfileL0(engine, agentID, slot)
}

// Default builds the first profile of a domain: a neutral assistant identity
// seeded with the current keyword distribution.
func Default(topTerms []string, totalEngrams uint32) *core.ProfileSlot {
	return &core.ProfileSlot{
		Name:        "Agent",
		Role:        "assistant",
		Personality: joinTopTerms(topTerms, 5),
		Preferences: map[string]string{
			"top_keywords":  joinTopTerms(topTerms, 20),
			"total_engrams": fmt.Sprintf("%d", totalEngrams),
		},
		Lexicon:         map[string]string{},
		StyleTraits:     []string{},
		EmotionPatterns: map[string]string{},
	}
}

// Samples ranks L1 nodes by Importance×exp(-lambda×age) and returns the top
// maxDistillSamples for distillation, plus the total node count.
func Samples(engine *core.StorageEngine, agentID uint64) ([]core.DistillSample, int) {
	nowMs := time.Now().UnixMilli()
	candidates := make([]core.DistillSample, 0)
	for _, node := range core.CollectAllSceneNodes(engine, agentID) {
		candidates = append(candidates, core.DistillSample{
			IDHash:     node.IDHash,
			Keywords:   sampleKeywords(engine, agentID, node.TopicIDs),
			Importance: node.Importance,
			UpdatedAt:  node.UpdatedAt,
		})
	}
	total := len(candidates)
	slices.SortFunc(candidates, func(a, b core.DistillSample) int {
		return cmp.Compare(SampleRank(b, nowMs), SampleRank(a, nowMs))
	})
	if len(candidates) > maxDistillSamples {
		candidates = candidates[:maxDistillSamples]
	}
	return candidates, total
}

// SampleRank is the recency-weighted importance of one distillation sample.
func SampleRank(s core.DistillSample, nowMs int64) float64 {
	return float64(s.Importance) * math.Exp(-distillSampleLambda*common.ElapsedHours(nowMs, s.UpdatedAt))
}

// MergeDistill writes the distilled emotion and MBTI signals into the profile
// without clearing other fields.
func MergeDistill(engine *core.StorageEngine, agentID uint64, emo core.EmotionScore, mbti core.MBTIScore) error {
	nowMs := time.Now().UnixMilli()
	slot, err := repo.GetProfileL0(engine, agentID)
	if err != nil {
		slot = Default(nil, 0)
	}
	applyDistill(slot, emo, mbti, nowMs)
	return repo.UpdateProfileL0(engine, agentID, slot)
}

func applyDistill(slot *core.ProfileSlot, emo core.EmotionScore, mbti core.MBTIScore, nowMs int64) {
	if slot.EmotionPatterns == nil {
		slot.EmotionPatterns = map[string]string{}
	}
	if slot.Preferences == nil {
		slot.Preferences = map[string]string{}
	}
	slot.EmotionPatterns["valence"] = ftoa(emo.Valence)
	slot.EmotionPatterns["arousal"] = ftoa(emo.Arousal)
	slot.EmotionPatterns["dominance"] = ftoa(emo.Dominance)
	slot.EmotionPatterns["updated_at_ms"] = strconv.FormatInt(nowMs, 10)
	slot.Preferences["mbti_type"] = mbti.Type
	slot.Preferences["mbti_i_e"] = ftoa(mbti.IE)
	slot.Preferences["mbti_n_s"] = ftoa(mbti.NS)
	slot.Preferences["mbti_t_f"] = ftoa(mbti.TF)
	slot.Preferences["mbti_j_p"] = ftoa(mbti.JP)
	slot.Preferences["mbti_updated_at_ms"] = strconv.FormatInt(nowMs, 10)
	slot.Personality = mbti.Type
}

func sampleKeywords(engine *core.StorageEngine, agentID uint64, topicIDs []uint64) []string {
	var kws []string
	for _, tid := range topicIDs {
		t, err := core.ReadTopicLenient(engine, agentID, tid)
		if err != nil || t == nil {
			continue
		}
		for _, kw := range t.FusedKeywords {
			if len(kws) >= maxDistillKeywordsPerSample {
				return kws
			}
			kws = append(kws, kw)
		}
	}
	return kws
}

func joinTopTerms(terms []string, n int) string {
	limit := min(n, len(terms))
	var result strings.Builder
	for i := range limit {
		if i > 0 {
			result.WriteString(", ")
		}
		result.WriteString(terms[i])
	}
	return result.String()
}

func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
