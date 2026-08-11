// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package sub

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
	"github.com/qyiun666/MemHop/internal/sub/repo/index"
)

// RunDream 跑一次完整 dream 管线：激活场景并行 L2 压缩，全部完成后统一
// L1 重建/衰减、L0 画像/蒸馏；重建后的 sparse 与 L1 反查索引直接装回 db。
// 任何阶段失败返回错误；无压缩组与无样本蒸馏视为正常。
func (db *DB) RunDream(ctx context.Context) (bool, error) {

	// 阶段 1：每个激活场景一个 goroutine 并行 L2 压缩（只写盘，索引末尾统一重建）。
	groups, failures := db.compressActiveScenes(ctx)
	if groups == 0 && failures > 0 {
		return false, fmt.Errorf("dream: LLM consolidation failed for all scenes")
	}
	if err := db.stageCancelled(ctx, "l2_compress"); err != nil {
		return false, err
	}

	// 阶段 2：压缩后统一重建检索索引（sparse/L1Reverse/L2Meta 一次扫盘）。
	newSparse, _, newL2Meta, err := index.RebuildSearchIndexes(db.engine)
	if err != nil {
		return false, err
	}
	decayParams := repo.DecayParams{
		LambdaNode:             float64(db.config.Defaults.LambdaNode),
		LambdaEdge:             float64(db.config.Defaults.LambdaEdge),
		NodeRemoveThreshold:    db.config.Defaults.NodeRemoveThreshold,
		NodePruneEdgeThreshold: db.config.Defaults.NodePruneEdgesThreshold,
		EdgeRemoveThreshold:    db.config.Defaults.EdgeRemoveThreshold,
		MinEdgeNodes:           db.config.Defaults.MinEdgeNodes,
	}

	// 阶段 3：L1 重建（清理 stale 节点）。
	if _, err := repo.RebuildL1FromL2(db.engine, newL2Meta, &decayParams); err != nil {
		return false, err
	}
	if err := db.stageCancelled(ctx, "l1_rebuild"); err != nil {
		return false, err
	}

	// 阶段 4：L1 时间衰减。
	if _, err := repo.DecayL1Network(db.engine, newL2Meta, &decayParams); err != nil {
		return false, err
	}
	if err := db.stageCancelled(ctx, "l1_decay"); err != nil {
		return false, err
	}

	// 阶段 5：L0 画像重建。
	if err := repo.GenerateProfileL0(db.engine, newSparse); err != nil {
		return false, err
	}
	if err := db.stageCancelled(ctx, "l0_profile"); err != nil {
		return false, err
	}

	// 阶段 6：L0 蒸馏（LLM 推导情绪/MBTI 并回填 L1 情感）。
	if err := db.distillL0Stage(ctx); err != nil {
		return false, err
	}

	// 收尾：L1 阶段删改节点后重建反查索引，一并装回 db。
	db.sparseIndex = newSparse
	db.l1Reverse.Store(index.BuildL1ReverseIndex(db.engine))
	return true, nil
}

// compressActiveScenes 每个激活场景一个 goroutine：读取该场景 depth1 话题，
// LLM 判断可合并分组并应用压缩。返回成功压缩的组数与 LLM 失败场景数。
func (db *DB) compressActiveScenes(ctx context.Context) (uint32, int) {
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		groups   uint32
		failures int
	)
	for _, sid := range db.activeScenes {
		wg.Add(1)
		go func(sceneID uint64) {
			defer wg.Done()
			topics, err := repo.ListTopicsL2(db.engine, common.FormatHash(sceneID), 1, 2)
			if err != nil {
				return
			}
			// 话题数低于压缩阈值则跳过：少量话题保留原始细节，不值得压缩。
			if len(topics) < db.config.Defaults.DreamCompressMinTopics {
				return
			}
			out, err := db.llm.Consolidate(ctx, topics)
			if err != nil {
				mu.Lock()
				failures++
				mu.Unlock()
				return
			}
			applied := db.applyGroups(ctx, sceneID, topics, out)
			mu.Lock()
			groups += applied
			mu.Unlock()
		}(sid)
	}
	wg.Wait()
	return groups, failures
}

// applyGroups 应用单个场景的压缩分组：每组先把 MergedSummary 存为 L4 压缩归档，
// 再对其提取关键词作为融合话题的关键词，创建 parent 话题并挂 summary 归档引用，
// 最后下沉组内节点。返回实际压缩的组数。
func (db *DB) applyGroups(ctx context.Context, sceneID uint64, topics []core.TopicSlot, out *ConsolidationOutput) uint32 {
	byID := make(map[uint64]core.TopicSlot, len(topics))
	for _, t := range topics {
		byID[t.ID] = t
	}
	var count uint32
	for _, g := range out.L2Groups {
		if len(g.NodeHashes) < 2 {
			continue
		}
		minTS, maxTS, ok := db.groupTimestamps(g.NodeHashes, byID)
		if !ok {
			continue
		}
		parentID := core.ComputeTopicID(sceneID, minTS, maxTS)
		parentIDStr := common.FormatHash(parentID)

		// 1. MergedSummary 存为 L4 梦境归档（role=RoleDream），保留全部细节原文。
		archiveID, err := repo.AppendArchiveL4(db.engine, parentIDStr, core.RoleDream, core.ContentText, g.MergedSummary, maxTS)
		if err != nil {
			slog.Warn("dream: archive merged summary failed", "parent", parentIDStr, "err", err)
			continue
		}

		// 2. 对 MergedSummary 提取关键词，作为融合话题的关键词（保留细节、供检索）。
		keywords, err := db.llm.ExtractKeywords(ctx, g.MergedSummary)
		if err != nil || len(keywords) == 0 {
			keywords = []string{g.MergedTitle}
		}

		// 3. 编码 MergedSummary 为质心向量并写入向量记录；编码失败即报错。
		centroidRef, err := db.writeCentroid(g.MergedSummary)
		if err != nil {
			slog.Warn("dream: encode merged summary centroid failed", "parent", parentIDStr, "err", err)
			continue
		}

		// 4. 创建融合话题（UserKeywords=FusedKeywords=提取的关键词，带质心向量）。
		if !repo.CreateFusedTopicL2(db.engine, common.FormatHash(sceneID), keywords, minTS, maxTS, g.NodeHashes, centroidRef) {
			slog.Warn("dream: create fused topic failed", "parent", parentIDStr)
			continue
		}
		// 5. 融合话题挂 summary 归档引用，检索到时可取回完整总结原文。
		if !repo.UpdateTopicL4RefsL2(db.engine, parentIDStr, []uint64{archiveID}) {
			slog.Warn("dream: attach summary archive ref failed", "parent", parentIDStr)
			continue
		}
		if _, err := repo.CompressTopicsL2(db.engine, g.NodeHashes, parentID); err != nil {
			slog.Warn("dream: compress child topics failed", "parent", parentIDStr, "err", err)
			continue
		}
		count++
	}
	return count
}

// groupTimestamps 取组内话题的最早用户时间与最晚 agent 时间。
func (db *DB) groupTimestamps(nodeHashes []uint64, byID map[uint64]core.TopicSlot) (minTS, maxTS int64, ok bool) {
	for _, id := range nodeHashes {
		t, found := byID[id]
		if !found {
			continue
		}
		if !ok || t.UserTimestamp < minTS {
			minTS = t.UserTimestamp
		}
		if !ok || t.AgentTimestamp > maxTS {
			maxTS = t.AgentTimestamp
		}
		ok = true
	}
	return minTS, maxTS, ok
}

// distillL0Stage 采样 L1 节点推导情绪与 MBTI 画像并回填情感；无样本时跳过。
func (db *DB) distillL0Stage(ctx context.Context) error {
	samples, _ := repo.SampleL1ForDistill(db.engine)
	if len(samples) == 0 {
		return nil
	}
	llmSamples := make([]L1Sample, len(samples))
	for i, s := range samples {
		llmSamples[i] = L1Sample{IDHash: s.IDHash, Keywords: s.Keywords, Importance: s.Importance}
	}
	out, err := db.llm.Distill(ctx, llmSamples)
	if err != nil {
		return err
	}
	if err := repo.MergeDistillIntoProfile(db.engine,
		repo.DistillEmotion{Valence: out.Emotion.Valence, Arousal: out.Emotion.Arousal, Dominance: out.Emotion.Dominance},
		repo.DistillMBTI{IE: out.MBTI.IE, NS: out.MBTI.NS, TF: out.MBTI.TF, JP: out.MBTI.JP, Type: out.MBTI.Type},
	); err != nil {
		return err
	}
	perNode := make(map[uint64]repo.L1NodeEmotion, len(out.PerNode))
	for _, n := range out.PerNode {
		id, err := common.ParseID(n.IDHex)
		if err != nil {
			continue
		}
		perNode[id] = repo.L1NodeEmotion{Valence: n.Valence, Arousal: n.Arousal}
	}
	repo.BackfillL1Emotions(db.engine, perNode)
	return nil
}

// stageCancelled 报告阶段间 ctx 取消。
func (db *DB) stageCancelled(ctx context.Context, stage string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("dream: cancelled after %s stage: %w", stage, err)
	}
	return nil
}
