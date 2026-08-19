package repo

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 profile operations: singleton ProfileSlot at the fixed ID hash("profile");
// GetProfileL0 returns ErrNotFound when absent.
func GetProfileL0(engine *core.StorageEngine) (*core.ProfileSlot, error) {
	slot, err := core.ReadProfileSlot(engine, common.HashID("profile"))
	if err != nil {
		return nil, common.NewError(common.ErrNotFound, "profile not found", err)
	}
	return slot, nil
}

func UpdateProfileL0(engine *core.StorageEngine, slot *core.ProfileSlot) error {
	slot.IDHash = common.HashID("profile")
	return core.WriteProfileSlot(engine, slot.IDHash, slot)
}

// maxDistillSamples bounds both prompt cost and LLM input size for L0
// distillation. 200 top-ranked nodes is far more signal than emotion/MBTI
// extraction needs.
const maxDistillSamples = 200

// maxDistillKeywordsPerSample bounds the keyword list sent for each node.
const maxDistillKeywordsPerSample = 20

// distillSampleLambda: sample-rank age decay per hour (ranking only,
// decoupled from the LambdaNode decay config).
const distillSampleLambda = 0.01

type L1DistillSample struct {
	IDHash     uint64
	Keywords   []string
	Importance float32
	UpdatedAt  int64
}

type DistillEmotion struct {
	Valence   float64
	Arousal   float64
	Dominance float64
}

type DistillMBTI struct {
	IE   float64
	NS   float64
	TF   float64
	JP   float64
	Type string
}

type L1NodeEmotion struct {
	Valence float64
	Arousal float64
}

// GenerateProfileL0 rebuilds the L0 profile from the sparse keyword
// distribution: creates a default profile if missing, else updates
// personality and keyword/memory fields.
func GenerateProfileL0(engine *core.StorageEngine, sparse *SparseIndex) error {
	topKeywords := sparse.TopTerms(20)
	topTerms := make([]string, len(topKeywords))
	for i, tk := range topKeywords {
		topTerms[i] = tk.Term
	}
	totalEngrams := engine.RecordCount()
	slot, err := GetProfileL0(engine)
	if err != nil {
		return UpdateProfileL0(engine, newDefaultProfile(topTerms, totalEngrams))
	}
	slot.Personality = joinTopTerms(topTerms, 5)
	if slot.Preferences == nil {
		slot.Preferences = map[string]string{}
	}
	slot.Preferences["top_keywords"] = joinTopTerms(topTerms, 20)
	slot.Preferences["total_engrams"] = fmt.Sprintf("%d", totalEngrams)
	return UpdateProfileL0(engine, slot)
}

func newDefaultProfile(topTerms []string, totalEngrams uint32) *core.ProfileSlot {
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

func joinTopTerms(terms []string, n int) string {
	limit := n
	if limit > len(terms) {
		limit = len(terms)
	}
	result := ""
	for i := 0; i < limit; i++ {
		if i > 0 {
			result += ", "
		}
		result += terms[i]
	}
	return result
}

// SampleL1ForDistill ranks L1 nodes by Importance×exp(-lambda×age) and
// returns the top maxDistillSamples plus the total node count.
func SampleL1ForDistill(engine *core.StorageEngine) ([]L1DistillSample, int) {
	nowMs := time.Now().UnixMilli()
	candidates := make([]L1DistillSample, 0)
	for _, node := range core.CollectAllSceneNodes(engine) {
		candidates = append(candidates, L1DistillSample{
			IDHash:     node.IDHash,
			Keywords:   collectSampleKeywords(engine, node.TopicIDs),
			Importance: node.Importance,
			UpdatedAt:  node.UpdatedAt,
		})
	}
	total := len(candidates)
	sort.Slice(candidates, func(i, j int) bool {
		return sampleRank(candidates[i], nowMs) > sampleRank(candidates[j], nowMs)
	})
	if len(candidates) > maxDistillSamples {
		candidates = candidates[:maxDistillSamples]
	}
	return candidates, total
}

func sampleRank(s L1DistillSample, nowMs int64) float64 {
	return float64(s.Importance) * math.Exp(-distillSampleLambda*dtHoursFrom(nowMs, s.UpdatedAt))
}

func collectSampleKeywords(engine *core.StorageEngine, topicIDs []uint64) []string {
	var kws []string
	for _, tid := range topicIDs {
		t, err := core.ReadTopicLenient(engine, tid)
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

// MergeDistillIntoProfile merges distill results into the profile without
// clearing other fields.
func MergeDistillIntoProfile(engine *core.StorageEngine, emo DistillEmotion, mbti DistillMBTI) error {
	nowMs := time.Now().UnixMilli()
	slot, err := GetProfileL0(engine)
	if err != nil {
		slot = newDefaultProfile(nil, 0)
	}
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
	return UpdateProfileL0(engine, slot)
}

// BackfillL1Emotions writes per-node emotions into L1 nodes lacking signals;
// returns the count written. A missing node or a write failure is returned
// as an error — nothing is silently skipped.
func BackfillL1Emotions(engine *core.StorageEngine, perNode map[uint64]L1NodeEmotion) (int, error) {
	written := 0
	for id, em := range perNode {
		node := readSceneNode(engine, id)
		if node == nil {
			return written, fmt.Errorf("backfill L1 emotions: node %s not found", common.FormatHash(id))
		}
		if node.Valence != 0 || node.Arousal != 0 {
			continue
		}
		node.Valence = em.Valence
		node.Arousal = em.Arousal
		node.UpdatedAt = time.Now().UnixMilli()
		if err := core.WriteSceneNode(engine, id, node); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
