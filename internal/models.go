// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// DTO aliases: the business request/response shapes live in the bottom
// model package (internal/repo/core/model_dto.go) so capability packages
// can consume them without importing the repository layer. The internal
// package keeps referring to them by their historical names.

package internal

import (
	"github.com/qyiun666/MemHop/internal/cap/llmops"
	"github.com/qyiun666/MemHop/internal/plan"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

type (
	SearchQuery              = core.SearchQuery
	SearchResult             = core.SearchResult
	TurnUpdate               = core.TurnUpdate
	SceneMessage             = core.SceneMessage
	SceneContextTopic        = core.SceneContextTopic
	SceneContext             = core.SceneContext
	ScenePatch               = core.ScenePatch
	L3Graph                  = core.L3Graph
	L3ImportItem             = core.L3ImportItem
	L3Relation               = core.L3Relation
	L3ImportResult           = core.L3ImportResult
	L3ImportMode             = core.L3ImportMode
	L3NodeQuery              = core.L3NodeQuery
	L3Subgraph               = core.L3Subgraph
	L4Query                  = core.L4Query
	CapabilityImport         = core.CapabilityImport
	CapabilityPatch          = core.CapabilityPatch
	CapabilityListQuery      = core.CapabilityListQuery
	TrajectorySessionSummary = core.TrajectorySessionSummary
	DreamReport              = core.DreamReport
	DreamStage               = core.DreamStage
	CrystallizeResult        = core.CrystallizeResult
	CrystallizeDetail        = core.CrystallizeDetail

	// Plan surface types live in the plan small-method package.
	PlanStatus   = plan.PlanStatus
	PlanTree     = plan.PlanTree
	PlanNode     = plan.PlanNode
	PlanNodeView = plan.PlanNodeView

	// LLM capability contracts (prompt inputs / parsed outputs) live in the
	// llmops capability package.
	L1Sample              = llmops.L1Sample
	L2Group               = llmops.L2Group
	NodeEmotion           = llmops.NodeEmotion
	EmotionScore          = llmops.EmotionScore
	MBTIScore             = llmops.MBTIScore
	DistillOutput         = llmops.DistillOutput
	ConsolidationOutput   = llmops.ConsolidationOutput
	CrystallizeCapability = llmops.CrystallizeCapability
	CrystallizeOutput     = llmops.CrystallizeOutput
)

// Import-mode constants of the L3 import policy (see core.L3ImportMode).
const (
	L3ImportSkip      = core.L3ImportSkip
	L3ImportMerge     = core.L3ImportMerge
	L3ImportOverwrite = core.L3ImportOverwrite
)

// Plan lifecycle constants (see plan.PlanStatus).
const (
	PlanPending    = plan.PlanPending
	PlanInProgress = plan.PlanInProgress
	PlanRunning    = plan.PlanRunning
	PlanDone       = plan.PlanDone
	PlanFailed     = plan.PlanFailed
)
