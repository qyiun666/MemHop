// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Re-export seam for the api facade: the api package must not import
// internal/repo/core or internal/common directly (dependency chain
// api → internal → repo → core), so every slot model, enum constant and
// error-code symbol that appears in the public surface is re-exported
// here as an alias/forwarder. Aliases are identity — no copying, no
// business logic.

package internal

import (
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/config"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ---- configuration types (real definitions in internal/config) ----

type (
	MemHopConfig   = config.MemHopConfig
	LlmConfig      = config.LlmConfig
	MemHopDefaults = config.MemHopDefaults
)

// DefaultMemHopDefaults is the shared default engine configuration. It is a
// value: pass it as-is, or copy it and edit the copy to tune one open.
var DefaultMemHopDefaults = config.DefaultMemHopDefaults

// ---- slot models (method signatures of Session / DB) ----

type (
	ProfileSlot    = core.ProfileSlot
	SceneSlot      = core.SceneSlot
	SceneNode      = core.SceneNode
	HypergraphSlot = core.HypergraphSlot
	HypergraphNode = core.HypergraphNode
	HypergraphEdge = core.HypergraphEdge
	ArchiveSlot    = core.ArchiveSlot
	ArchiveKind    = core.ArchiveKind
	GraphEdgeKind  = core.GraphEdgeKind
	TopicSlot      = core.TopicSlot
	ContentType    = core.ContentType
)

// NewError re-exported so the api facade can build domain errors without
// importing internal/common.
var NewError = common.NewError

// ---- error contract ----

// Code is the numeric error-code type carried inside Error.
type Code = common.Code

// CodeOf extracts the numeric error code of err (0 when it is not a MemHop Error).
func CodeOf(err error) Code { return common.CodeOf(err) }

// Error codes; see internal/common/errors.go for the interval contract.
const (
	ErrConfig          = common.ErrConfig
	ErrInvalidQuery    = common.ErrInvalidQuery
	ErrNotFound        = common.ErrNotFound
	ErrAgentNotFound   = common.ErrAgentNotFound
	ErrIO              = common.ErrIO
	ErrClosed          = common.ErrClosed
	ErrInvalidMagic    = common.ErrInvalidMagic
	ErrCRCMismatch     = common.ErrCRCMismatch
	ErrCorruption      = common.ErrCorruption
	ErrSerialization   = common.ErrSerialization
	ErrDeserialization = common.ErrDeserialization
	ErrCancelled       = common.ErrCancelled
	ErrLLM             = common.ErrLLM
)

// ---- L3 edge kind constants ----

const (
	EdgeRelated    = core.EdgeRelated
	EdgeCausal     = core.EdgeCausal
	EdgePartOf     = core.EdgePartOf
	EdgeSequence   = core.EdgeSequence
	EdgeDependency = core.EdgeDependency
	EdgeCustom     = core.EdgeCustom
)

// ---- L4 message role constants ----

const (
	RoleUser   = core.RoleUser
	RoleAgent  = core.RoleAgent
	RoleSystem = core.RoleSystem
)

// ---- agent domain identity ----

const (
	AgentTypePrimary = core.AgentTypePrimary
	AgentTypeSub     = core.AgentTypeSub
)

// ---- L4 content kind constants ----

const (
	KindUtterance = core.KindUtterance
	KindEvent     = core.KindEvent
)

// ---- L4 content type constants ----

const (
	ContentText     = core.ContentText
	ContentImage    = core.ContentImage
	ContentVideo    = core.ContentVideo
	ContentDocument = core.ContentDocument
	ContentAudio    = core.ContentAudio
	ContentCode     = core.ContentCode
	ContentOther    = core.ContentOther
)

// ---- external id rendering ----

// FormatID renders any record or domain ID as its external 16-char hex form —
// the only id shape the facade exchanges with a host, since every id is issued
// by the library (Search mints turn ids).
func FormatID(id uint64) string { return common.FormatHash(id) }

// parseID is that boundary's other direction: it reads one host-supplied hex id
// back into the numeric form, naming which field it came from. Every id handed in
// by a host crosses into the library through here, so no layer below the
// composition root parses an id string.
func parseID(field, hexID string) (uint64, error) {
	id, err := common.ParseID(hexID)
	if err != nil {
		return 0, common.NewError(common.ErrInvalidQuery, "parse "+field+" id", err)
	}
	return id, nil
}
