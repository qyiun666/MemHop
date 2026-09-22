// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// DTO aliases: the business request/response shapes live in the bottom
// model package (internal/repo/core/model_dto.go) so the internal/cap
// packages can consume them without importing the repository layer. The
// internal package keeps referring to them by their historical names.

package internal

import (
	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/plan"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

type (
	SearchQuery       = core.SearchQuery
	SearchResult      = core.SearchResult
	TurnEnd           = core.TurnEnd
	SceneMessage      = core.SceneMessage
	SceneContextTopic = core.SceneContextTopic
	SceneContext      = core.SceneContext
	ScenePatch        = core.ScenePatch
	L3Graph           = core.L3Graph
	L3ImportItem      = core.L3ImportItem
	L3Relation        = core.L3Relation
	L3ImportResult    = core.L3ImportResult
	L3ImportMode      = core.L3ImportMode
	L3NodeQuery       = core.L3NodeQuery
	L3Subgraph        = core.L3Subgraph
	L4Query           = core.L4Query
	DreamReport       = core.DreamReport
	DreamStage        = core.DreamStage

	PlanStatus   = plan.PlanStatus
	PlanTree     = plan.PlanTree
	PlanStep     = plan.Step
	PlanNodeView = plan.PlanNodeView

	EmotionScore = llmops.EmotionScore
	MBTIScore    = llmops.MBTIScore
)

const (
	L3ImportSkip      = core.L3ImportSkip
	L3ImportMerge     = core.L3ImportMerge
	L3ImportOverwrite = core.L3ImportOverwrite
)

const (
	PlanInProgress = plan.PlanInProgress
	PlanDone       = plan.PlanDone
	PlanFailed     = plan.PlanFailed
)
