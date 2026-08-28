// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Profile distillation policy (moved out of the record layer so the
// repository keeps record reads/writes only): Dream distills emotion/MBTI
// from L1 samples into the typed L0 profile signals; identity fields stay
// host-authored.

package profile

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
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

// Default builds the first profile of a domain: a neutral assistant
// identity. Name/Role/Preferences are host-owned; Personality is seeded by
// the host and evolved by Dream distillation.
func Default() *core.ProfileSlot {
	return &core.ProfileSlot{
		Name:        "Agent",
		Role:        "assistant",
		Preferences: map[string]string{},
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

// MergeDistill writes the distilled emotion, MBTI and personality summary
// into the profile without touching host-owned fields (Name/Role/
// Preferences). An empty personality keeps the host-seeded value untouched.
func MergeDistill(engine *core.StorageEngine, agentID uint64, emo core.EmotionScore, mbti core.MBTIScore, personality string) error {
	slot, err := repo.GetProfileL0(engine, agentID)
	if err != nil {
		slot = Default()
	}
	slot.EmotionState = emo
	slot.MBTI = mbti
	if personality != "" {
		slot.Personality = personality
	}
	slot.UpdatedAtMs = time.Now().UnixMilli()
	return repo.UpdateProfileL0(engine, agentID, slot)
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
