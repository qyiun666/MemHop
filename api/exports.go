// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Re-exported data-model surface of the facade: enum constants and the error
// contract. Response slot models are real structs in api/types.go so their
// IDs are surfaced as 16-char hex strings.

package api

import "github.com/qyiun666/MemHop/internal"

// ---- L5 capability enum constants ----

const (
	CapabilityMCP       = internal.CapabilityMCP
	CapabilitySkill     = internal.CapabilitySkill
	CapabilityAPI       = internal.CapabilityAPI
	CapabilityComposite = internal.CapabilityComposite
)

const (
	CapabilityDraft      = internal.CapabilityDraft
	CapabilityActive     = internal.CapabilityActive
	CapabilityDeprecated = internal.CapabilityDeprecated
)

const (
	CapabilityOriginImported     = internal.CapabilityOriginImported
	CapabilityOriginCrystallized = internal.CapabilityOriginCrystallized
	CapabilityOriginHost         = internal.CapabilityOriginHost
	CapabilityOriginBuiltin      = internal.CapabilityOriginBuiltin
)

// ---- L3 edge kind constants ----

const (
	EdgeRelated    = internal.EdgeRelated
	EdgeCausal     = internal.EdgeCausal
	EdgePartOf     = internal.EdgePartOf
	EdgeSequence   = internal.EdgeSequence
	EdgeDependency = internal.EdgeDependency
	EdgeCustom     = internal.EdgeCustom
)

// ---- L3 import mode constants ----

const (
	L3ImportSkip      = internal.L3ImportSkip
	L3ImportMerge     = internal.L3ImportMerge
	L3ImportOverwrite = internal.L3ImportOverwrite
)

// ---- L4 content type constants ----

const (
	ContentText     = internal.ContentText
	ContentImage    = internal.ContentImage
	ContentVideo    = internal.ContentVideo
	ContentDocument = internal.ContentDocument
	ContentAudio    = internal.ContentAudio
	ContentCode     = internal.ContentCode
	ContentOther    = internal.ContentOther
)
