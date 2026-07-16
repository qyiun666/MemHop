// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/encoder"
	"github.com/qyiun666/memhop/memhop/internal/core/index"
	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
)

// DreamReport holds the results of a dream pipeline run.
type DreamReport struct {
	ConsolidatedCount uint32        `json:"consolidated_count"`
	NewL3Nodes        uint32        `json:"new_l3_nodes"`
	NewCrystals       uint32        `json:"new_crystals"`
	PrunedCrystals    uint32        `json:"pruned_crystals"`
	L1DecayedNodes    uint32        `json:"l1_decayed_nodes"`
	L1PrunedEdges     uint32        `json:"l1_pruned_edges"`
	L1RemovedNodes    uint32        `json:"l1_removed_nodes"`
	L1RemovedEdges    uint32        `json:"l1_removed_edges"`
	Stages            []StageReport `json:"stages"`
}

// StageReport holds per-stage execution info.
type StageReport struct {
	Name           string `json:"name"`
	Status         string `json:"status"` // "success" | "failed" | "skipped"
	Description    string `json:"description"`
	ProcessedCount int    `json:"processed_count"`
	DurationMs     int64  `json:"duration_ms"`
	Error          string `json:"error,omitempty"`
}

// DreamPipeline runs the full consolidated dream pipeline.
func DreamPipeline(
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
	llm LlmProvider,
	l2IDs []uint64,
	decayCfg *core.DecayConfig,
	l2Meta *index.L2MetaIndex,
	enc encoder.Encoder,
) (*DreamReport, error) {
	targetIDs := resolveTargetIDs(l2IDs, engine)
	stages := make([]StageReport, 0, 4)

	// Phase 1: collect data + LLM call.
	input := buildConsolidationInput(engine, l2Meta, targetIDs)
	llmOutput, err := llm.Consolidate(input)
	if err != nil {
		return nil, fmt.Errorf("dream: LLM consolidation failed: %w", err)
	}

	// Phase 2: apply results
	var metrics pipelineMetrics
	decayParams := DecayParamsFromConfig(decayCfg)
	stages = applyL2Stage(llmOutput, engine, sparseIdx, l2Meta, enc, stages, &metrics)
	stages = rebuildL1Stage(engine, sparseIdx, l2Meta, decayParams, stages, &metrics)
	stages = decayL1Stage(engine, sparseIdx, decayParams, l2Meta, stages, &metrics)
	stages = profileL0Stage(engine, sparseIdx, stages)

	report := buildDreamReport(stages, &metrics)
	return report, nil
}

type pipelineMetrics struct {
	l2Affected     uint32
	l1Decayed      uint32
	l1PrunedEdges  uint32
	l1RemovedNodes uint32
	l1RemovedEdges uint32
	l1Updated      uint32
}

func resolveTargetIDs(l2IDs []uint64, engine *storage.StorageEngine) map[uint64]bool {
	if len(l2IDs) > 0 {
		m := make(map[uint64]bool, len(l2IDs))
		for _, id := range l2IDs {
			m[id] = true
		}
		return m
	}
	return collectAllL2IDs(engine)
}

func collectAllL2IDs(engine *storage.StorageEngine) map[uint64]bool {
	ids := make(map[uint64]bool)
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, _, err := engine.ReadRecord(idHash)
		if err == nil && rt == storage.RecL2Topic {
			ids[idHash] = true
		}
		return true
	})
	return ids
}

func buildConsolidationInput(
	engine *storage.StorageEngine,
	l2Meta *index.L2MetaIndex,
	targetIDs map[uint64]bool,
) *ConsolidationInput {
	sceneMap := make(map[uint64][]L2NodeData)
	for id := range targetIDs {
		topic, err := readTopic(engine, id)
		if err != nil || topic == nil {
			continue
		}
		meta := l2Meta.Get(id)
		sceneID := uint64(0)
		if meta != nil {
			sceneID = meta.SceneID
		}
		sceneMap[sceneID] = append(sceneMap[sceneID], topicToL2NodeData(topic))
	}

	scenes := buildScenes(sceneMap)

	return &ConsolidationInput{
		Scenes: scenes,
	}
}

func topicToL2NodeData(t *model.TopicSlot) L2NodeData {
	return L2NodeData{
		IDHash:        t.ID,
		CreatedAt:     t.CreatedAt,
		Depth:         t.Depth,
		UserKeywords:  t.UserKeywords,
		AgentKeywords: t.AgentKeywords,
		FusedKeywords: t.FusedKeywords,
		FusedSummary:  t.FusedSummary,
	}
}

func buildScenes(sceneMap map[uint64][]L2NodeData) []SceneData {
	var scenes []SceneData
	for sceneID, nodes := range sceneMap {
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].CreatedAt < nodes[j].CreatedAt })
		scenes = append(scenes, SceneData{SceneID: sceneID, Nodes: nodes})
	}
	sort.Slice(scenes, func(i, j int) bool { return scenes[i].SceneID < scenes[j].SceneID })
	return scenes
}

func applyL2Stage(
	out *ConsolidationOutput,
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
	l2Meta *index.L2MetaIndex,
	enc encoder.Encoder,
	stages []StageReport,
	m *pipelineMetrics,
) []StageReport {
	// Section-level parse failures are tolerated (partial LLM results are better
	// than skipping the entire Dream), but they must be observable in the report.
	if out.L2Groups.Status == SectionParseFailed {
		slog.Warn("dream: LLM l2_groups output parse failed, L2 compression skipped",
			"error", out.L2Groups.ParseError)
		return append(stages, StageReport{
			Name: "l2_compress", Status: "failed",
			Description: "LLM l2_groups output parse failed",
			Error:       out.L2Groups.ParseError,
		})
	}
	if out.L2Groups.Status != SectionValid {
		return stages
	}
	start := time.Now()
	cr, err := ApplyL2Groups(out.L2Groups.Value, engine, sparseIdx, l2Meta, enc)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return append(stages, failStage("l2_compress", "L2 merge failed", elapsed, err))
	}
	total := cr.GroupsDetected + cr.NodesMerged + cr.ParentsCreated + cr.NodesSunk + cr.NodesRemoved
	m.l2Affected += total
	desc := fmt.Sprintf("%d groups, %d merged, %d parents, %d sunk, %d removed",
		cr.GroupsDetected, cr.NodesMerged, cr.ParentsCreated, cr.NodesSunk, cr.NodesRemoved)
	return append(stages, StageReport{
		Name: "l2_compress", Status: "success",
		Description:    desc,
		ProcessedCount: int(total), DurationMs: elapsed,
	})
}

func rebuildL1Stage(
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
	l2Meta *index.L2MetaIndex,
	params *DecayParams,
	stages []StageReport,
	m *pipelineMetrics,
) []StageReport {
	start := time.Now()
	updated, err := RebuildL1FromL2(engine, sparseIdx, l2Meta, params)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return append(stages, failStage("l1_rebuild", "L1 rebuild failed", elapsed, err))
	}
	m.l1Updated += uint32(len(updated))
	return append(stages, StageReport{
		Name: "l1_rebuild", Status: "success",
		Description:    fmt.Sprintf("Rebuilt %d L1 associations", len(updated)),
		ProcessedCount: len(updated), DurationMs: elapsed,
	})
}

func decayL1Stage(
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
	params *DecayParams,
	l2Meta *index.L2MetaIndex,
	stages []StageReport,
	m *pipelineMetrics,
) []StageReport {
	start := time.Now()
	dr, err := DecayL1Network(engine, params, l2Meta, sparseIdx)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return append(stages, failStage("l1_decay", "L1 decay failed", elapsed, err))
	}
	m.l1Decayed += uint32(dr.DecayedNodes)
	m.l1PrunedEdges += uint32(dr.PrunedEdges)
	m.l1RemovedNodes += uint32(dr.RemovedNodes)
	m.l1RemovedEdges += uint32(dr.RemovedEdges)
	return append(stages, StageReport{
		Name: "l1_decay", Status: "success",
		Description:    fmt.Sprintf("L1 decay: %d nodes, %d edges", dr.DecayedNodes, dr.PrunedEdges),
		ProcessedCount: dr.DecayedNodes + dr.PrunedEdges + dr.RemovedNodes + dr.RemovedEdges,
		DurationMs:     elapsed,
	})
}

func profileL0Stage(
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
	stages []StageReport,
) []StageReport {
	start := time.Now()
	err := GenerateProfile(engine, sparseIdx)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return append(stages, failStage("l0_profile", "L0 profile generation failed", elapsed, err))
	}
	return append(stages, StageReport{
		Name: "l0_profile", Status: "success",
		Description: "L0 profile regenerated", ProcessedCount: 1, DurationMs: elapsed,
	})
}

func buildDreamReport(stages []StageReport, m *pipelineMetrics) *DreamReport {
	consolidated := m.l2Affected +
		m.l1Decayed + m.l1PrunedEdges + m.l1RemovedNodes + m.l1RemovedEdges + m.l1Updated
	return &DreamReport{
		ConsolidatedCount: consolidated,
		NewL3Nodes:        0,
		NewCrystals:       0,
		PrunedCrystals:    0,
		L1DecayedNodes:    m.l1Decayed,
		L1PrunedEdges:     m.l1PrunedEdges,
		L1RemovedNodes:    m.l1RemovedNodes,
		L1RemovedEdges:    m.l1RemovedEdges,
		Stages:            stages,
	}
}

func failStage(name, desc string, elapsed int64, err error) StageReport {
	return StageReport{
		Name: name, Status: "failed", Description: desc,
		DurationMs: elapsed, Error: err.Error(),
	}
}
