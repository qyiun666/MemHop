// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"fmt"
	"sort"
	"time"

	"github.com/qyiun666/memhop/memhop/internal/core"
	"github.com/qyiun666/memhop/memhop/internal/core/encoder"
	"github.com/qyiun666/memhop/memhop/internal/core/index"
	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
)

const maxRecentDialogues = 30

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
	stages := make([]StageReport, 0, 8)

	// Phase 1: collect data + LLM call
	input := buildConsolidationInput(engine, l2Meta, targetIDs)
	llmOutput, err := runConsolidation(llm, input)
	if err != nil {
		return nil, err
	}

	// Phase 2: apply results
	var metrics pipelineMetrics
	stages = applyL2Stage(llmOutput, engine, sparseIdx, l2Meta, enc, stages, &metrics)
	stages = applyL3Stage(llmOutput, engine, sparseIdx, stages, &metrics)
	stages = applyHabitStage(llmOutput, engine, stages, &metrics)
	stages = applyCrystalStage(llmOutput, engine, stages, &metrics)
	stages = rebuildL1Stage(engine, sparseIdx, l2Meta, stages, &metrics)
	stages = decayL1Stage(engine, decayCfg, l2Meta, stages, &metrics)
	stages = profileL0Stage(engine, sparseIdx, stages)
	stages = pruneCrystalStage(engine, stages, &metrics)

	report := buildDreamReport(stages, &metrics)
	return report, nil
}

type pipelineMetrics struct {
	l2Affected    uint32
	newL3Nodes    uint32
	newCrystals   uint32
	prunedCrystals uint32
	l1Decayed     uint32
	l1PrunedEdges uint32
	l1RemovedNodes uint32
	l1RemovedEdges uint32
	l1Updated     uint32
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
	dialogues := ExtractRecentDialogues(engine, maxRecentDialogues)
	chains := ExtractExistingChains(engine)

	return &ConsolidationInput{
		Scenes:          scenes,
		RecentDialogues: dialogues,
		ExistingChains:  chains,
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
		ChildrenIDs:   t.ChildrenIDs,
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

func runConsolidation(llm LlmProvider, input *ConsolidationInput) (*ConsolidationOutput, error) {
	output, err := llm.Consolidate(input)
	if err != nil {
		// LLM call itself failed (timeout, network, etc.)
		// Return an output with all sections as Empty so downstream stages can proceed
		return &ConsolidationOutput{
			L2Groups:      NewEmptySection[[]L2Group](),
			L3Extractions: NewEmptySection[[]L3Extraction](),
			Habits:        NewEmptySection[HabitAnalysis](),
			Crystals:      NewEmptySection[[]CrystalDef](),
		}, nil
	}
	// Section-level parse failures are tolerated: failed sections are treated as Empty
	// so that valid sections (e.g. habit analysis) still get applied.
	// This is intentional: partial LLM results are better than skipping the entire Dream.
	return output, nil
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

func applyL3Stage(
	out *ConsolidationOutput,
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
	stages []StageReport,
	m *pipelineMetrics,
) []StageReport {
	if out.L3Extractions.Status != SectionValid {
		return stages
	}
	start := time.Now()
	ids, err := ApplyL3Extractions(out.L3Extractions.Value, engine, sparseIdx)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return append(stages, failStage("l3_distill", "L3 distillation write failed", elapsed, err))
	}
	m.newL3Nodes += uint32(len(ids))
	return append(stages, StageReport{
		Name: "l3_distill", Status: "success",
		Description:    fmt.Sprintf("Distilled %d L3 nodes", len(ids)),
		ProcessedCount: len(ids), DurationMs: elapsed,
	})
}

func applyHabitStage(
	out *ConsolidationOutput,
	engine *storage.StorageEngine,
	stages []StageReport,
	m *pipelineMetrics,
) []StageReport {
	if out.Habits.Status != SectionValid {
		return stages
	}
	start := time.Now()
	hr, err := MergeHabitsIntoProfile(engine, &out.Habits.Value)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return append(stages, failStage("habit_distill", "Habit merge failed", elapsed, err))
	}
	total := hr.NewLexicon + hr.NewStyle + hr.NewEmotion
	return append(stages, StageReport{
		Name: "habit_distill", Status: "success",
		Description:    fmt.Sprintf("Habits: %d lexicon, %d style, %d emotion", hr.NewLexicon, hr.NewStyle, hr.NewEmotion),
		ProcessedCount: total, DurationMs: elapsed,
	})
}

func applyCrystalStage(
	out *ConsolidationOutput,
	engine *storage.StorageEngine,
	stages []StageReport,
	m *pipelineMetrics,
) []StageReport {
	if out.Crystals.Status != SectionValid {
		return stages
	}
	start := time.Now()
	ids, err := ApplyCrystals(out.Crystals.Value, engine)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return append(stages, failStage("l5_crystallize", "Crystal write failed", elapsed, err))
	}
	m.newCrystals += uint32(len(ids))
	return append(stages, StageReport{
		Name: "l5_crystallize", Status: "success",
		Description:    fmt.Sprintf("Crystallized %d patterns", len(ids)),
		ProcessedCount: len(ids), DurationMs: elapsed,
	})
}

func rebuildL1Stage(
	engine *storage.StorageEngine,
	sparseIdx *index.SparseIndex,
	l2Meta *index.L2MetaIndex,
	stages []StageReport,
	m *pipelineMetrics,
) []StageReport {
	start := time.Now()
	updated, err := RebuildL1FromL2(engine, sparseIdx, l2Meta)
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
	decayCfg *core.DecayConfig,
	l2Meta *index.L2MetaIndex,
	stages []StageReport,
	m *pipelineMetrics,
) []StageReport {
	start := time.Now()
	params := DecayParamsFromConfig(decayCfg)
	dr, err := DecayL1Network(engine, params, l2Meta)
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

func pruneCrystalStage(
	engine *storage.StorageEngine,
	stages []StageReport,
	m *pipelineMetrics,
) []StageReport {
	start := time.Now()
	pruned, err := PruneLowQualityCrystals(engine)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return append(stages, failStage("crystal_prune", "Crystal pruning failed", elapsed, err))
	}
	m.prunedCrystals += uint32(len(pruned))
	return append(stages, StageReport{
		Name: "crystal_prune", Status: "success",
		Description:    fmt.Sprintf("Pruned %d low-quality crystals", len(pruned)),
		ProcessedCount: len(pruned), DurationMs: elapsed,
	})
}

func buildDreamReport(stages []StageReport, m *pipelineMetrics) *DreamReport {
	consolidated := m.l2Affected + m.newL3Nodes + m.newCrystals + m.prunedCrystals +
		m.l1Decayed + m.l1PrunedEdges + m.l1RemovedNodes + m.l1RemovedEdges + m.l1Updated
	return &DreamReport{
		ConsolidatedCount: consolidated,
		NewL3Nodes:        m.newL3Nodes,
		NewCrystals:       m.newCrystals,
		PrunedCrystals:    m.prunedCrystals,
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

// suppress unused import warnings
var _ = sort.Slice
