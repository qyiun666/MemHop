// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Profile distillation policy: the emotion/MBTI distilled from L1 samples into
// the typed L0 signals, and the ranking that picks those samples. The identity
// fields of a profile are not this file's to write.

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

// defaultProfile builds the first profile of a domain: a neutral assistant
// identity with nothing distilled onto it yet.
func defaultProfile() *core.ProfileSlot {
	return &core.ProfileSlot{
		Name:        "Agent",
		Role:        "assistant",
		Preferences: map[string]string{},
	}
}

// Samples ranks L1 nodes by Importance×exp(-lambda×age) and returns the top
// maxDistillSamples for distillation. Ranking runs on the node fields alone: the
// keywords a sample carries come from its topics, one record read each, so
// collecting them before the cut would price the whole L1 set for the 200 rows
// that survive it.
func Samples(engine *core.StorageEngine, agentID uint64) []core.DistillSample {
	nowMs := time.Now().UnixMilli()
	nodes := core.CollectAllSceneNodes(engine, agentID)
	slices.SortFunc(nodes, func(a, b core.SceneNode) int {
		return cmp.Compare(sampleRank(&b, nowMs), sampleRank(&a, nowMs))
	})
	if len(nodes) > maxDistillSamples {
		nodes = nodes[:maxDistillSamples]
	}
	samples := make([]core.DistillSample, 0, len(nodes))
	for i := range nodes {
		keywords := sampleKeywords(engine, agentID, nodes[i].TopicIDs)
		if len(keywords) == 0 {
			// A row with no keywords is no signal, and the prompt asks the model to
			// infer an emotional state from what each row carries: a node whose topics
			// would not read back must thin the sample set, not join it as an empty one.
			continue
		}
		samples = append(samples, core.DistillSample{
			IDHash:     nodes[i].IDHash,
			Keywords:   keywords,
			Importance: nodes[i].Importance,
		})
	}
	return samples
}

// sampleRank is the recency-weighted importance of one candidate node.
func sampleRank(node *core.SceneNode, nowMs int64) float64 {
	return float64(node.Importance) * math.Exp(-distillSampleLambda*common.ElapsedHours(nowMs, node.UpdatedAt))
}

// MergeDistill writes the distilled emotion, MBTI and personality summary into
// the profile, leaving Name/Role/Preferences as stored. An empty personality
// keeps the value already on the slot.
func MergeDistill(engine *core.StorageEngine, agentID uint64, emo core.EmotionScore, mbti core.MBTIScore, personality string) error {
	slot, err := repo.GetProfileL0(engine, agentID)
	if err != nil {
		// Only a profile that was never written seeds a default one: treating a
		// transient read failure as "absent" would rewrite the profile from an
		// empty slot and drop the identity fields.
		if common.CodeOf(err) != common.ErrNotFound {
			return err
		}
		slot = defaultProfile()
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
