// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Public type surface of the MemHop facade: every alias forwards to the
// internal package (method signatures of the handles); this package never
// imports internal subpackages directly. Enum constants live in exports.go,
// the error contract in errors.go, id rendering in ids.go.

package api

import "github.com/qyiun666/MemHop/internal"

// ---- config / encoder ----

type (
	// MemHopConfig configures a MemHop database; nested fields (LLM, Defaults)
	// are assigned by field access, see DefaultMemHopDefaults.
	MemHopConfig = internal.MemHopConfig
	// Encoder is the embedding encoder contract required by OpenWithEncoder.
	Encoder = internal.Encoder
	// LlmConfig holds LLM provider settings; exported so hosts can build
	// MemHopConfig.LLM by literal instead of field-by-field assignment.
	LlmConfig = internal.LlmConfig
	// MemHopDefaults holds the host-facing business knobs (Capacity,
	// DreamCompressMinTopics, SearchDreamContextThreshold); engine tuning
	// constants are package-private. Exported so hosts can name the type
	// instead of copying DefaultMemHopDefaults.
	MemHopDefaults = internal.MemHopDefaults
)

// DefaultMemHopDefaults is the shared default engine configuration; assign
// it to MemHopConfig.Defaults without naming the nested type.
var DefaultMemHopDefaults = internal.DefaultMemHopDefaults

// ---- internal-layer types (method signatures) ----

type (
	SearchQuery              = internal.SearchQuery
	SearchResult             = internal.SearchResult
	SceneContext             = internal.SceneContext
	L3Graph                  = internal.L3Graph
	L3ImportItem             = internal.L3ImportItem
	L3ImportMode             = internal.L3ImportMode
	L3ImportResult           = internal.L3ImportResult
	L3NodeQuery              = internal.L3NodeQuery
	L3Subgraph               = internal.L3Subgraph
	L4Query                  = internal.L4Query
	CapabilityListQuery      = internal.CapabilityListQuery
	CapabilityPatch          = internal.CapabilityPatch
	CrystallizeResult        = internal.CrystallizeResult
	CrystallizeDetail        = internal.CrystallizeDetail
	TrajectoryStats          = internal.TrajectoryStats
	TrajectorySessionSummary = internal.TrajectorySessionSummary
	DreamReport              = internal.DreamReport
	DreamStage               = internal.DreamStage
)

// ---- L3 import mode constants ----

const (
	L3ImportSkip      = internal.L3ImportSkip
	L3ImportMerge     = internal.L3ImportMerge
	L3ImportOverwrite = internal.L3ImportOverwrite
)
