package repo

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 画像操作：单例 ProfileSlot，固定 ID = hash("profile")（与 crud/l0_ops.go
// 既有约定一致）。外部接口更新字段与 dream 蒸馏更新共用 UpdateProfileL0。
// GetProfileL0 读取 L0 画像单例，不存在返回 ErrNotFound。
func GetProfileL0(engine *core.StorageEngine) (*core.ProfileSlot, error) {
	slot, err := core.ReadProfileSlot(engine, common.HashID("profile"))
	if err != nil {
		return nil, common.NewError(common.ErrNotFound, "profile not found", err)
	}
	return slot, nil
}

// UpdateProfileL0 全量覆盖写回画像单例（ID 强制为固定 ID）。
func UpdateProfileL0(engine *core.StorageEngine, slot *core.ProfileSlot) error {
	slot.IDHash = common.HashID("profile")
	return core.WriteProfileSlot(engine, slot.IDHash, slot)
}

// ============================================================================
// Dream 辅助：L0 画像生成与蒸馏存储侧
// ============================================================================

// maxDistillSamples 是送入蒸馏 LLM 的 L1 节点样本上限。
const maxDistillSamples = 1000

// distillSampleLambda 是样本排序的年龄衰减率（每小时），仅用于排序，
// 与衰减配置 LambdaNode 解耦。
const distillSampleLambda = 0.01

// L1DistillSample 是蒸馏采样的 L1 节点（关键词取自关联话题）。
type L1DistillSample struct {
	IDHash     uint64
	Keywords   []string
	Importance float32
	UpdatedAt  int64
}

// DistillEmotion 是蒸馏输出的情绪三维度（[0,1]）。
type DistillEmotion struct {
	Valence   float64
	Arousal   float64
	Dominance float64
}

// DistillMBTI 是蒸馏输出的 MBTI 四维度（[-1,1]）与推导类型。
type DistillMBTI struct {
	IE   float64
	NS   float64
	TF   float64
	JP   float64
	Type string
}

// L1NodeEmotion 是单个 L1 节点的情感回填值。
type L1NodeEmotion struct {
	Valence float64
	Arousal float64
}

// GenerateProfileL0 从稀疏索引关键词分布重建 L0 画像：不存在时创建默认
// 画像，存在时更新人格与关键词/记忆量字段。
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

// SampleL1ForDistill 按 Importance×exp(-lambda×age) 排序取前 maxDistillSamples
// 个 L1 节点，返回样本与节点总数。
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
		kws = append(kws, t.FusedKeywords...)
	}
	return kws
}

// MergeDistillIntoProfile 把蒸馏结果合并进 L0 画像（不清空其他字段）。
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

// BackfillL1Emotions 把蒸馏的单节点情感写入尚无情感信号的 L1 节点，
// 返回写入数。
func BackfillL1Emotions(engine *core.StorageEngine, perNode map[uint64]L1NodeEmotion) int {
	written := 0
	for id, em := range perNode {
		node := readSceneNode(engine, id)
		if node == nil {
			continue
		}
		if node.Valence != 0 || node.Arousal != 0 {
			continue
		}
		node.Valence = em.Valence
		node.Arousal = em.Arousal
		node.UpdatedAt = time.Now().UnixMilli()
		if core.WriteSceneNode(engine, id, node) == nil {
			written++
		}
	}
	return written
}

func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
