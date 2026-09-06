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
	"github.com/qyiun666/MemHop/internal/cap/capability"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/config"
	"github.com/qyiun666/MemHop/internal/plan"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// ---- configuration types (real definitions in internal/config) ----

type (
	MemHopConfig   = config.MemHopConfig
	LlmConfig      = config.LlmConfig
	MemHopDefaults = config.MemHopDefaults
)

// DefaultMemHopDefaults is the shared default engine configuration; assign
// it to MemHopConfig.Defaults without naming the nested type.
var DefaultMemHopDefaults = config.DefaultMemHopDefaults

// ---- slot models (method signatures of Session / DB) ----

type (
	ProfileSlot      = core.ProfileSlot
	SceneSlot        = core.SceneSlot
	HypergraphSlot   = core.HypergraphSlot
	HypergraphNode   = core.HypergraphNode
	HypergraphEdge   = core.HypergraphEdge
	HypergraphSource = core.HypergraphSource
	ArchiveSlot      = core.ArchiveSlot
	TrajectorySlot   = core.TrajectorySlot
	GraphEdgeKind    = core.GraphEdgeKind
	TopicSlot        = core.TopicSlot
	ResourceRef      = capability.ResourceRef
	ContentType      = core.ContentType
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
	ErrLLM             = common.ErrLLM
)

// ---- L5 capability surface ----

// The engine stores no capability records: a host owns its capability
// directory and reuses the v4 parser as the disk format's single source of
// truth. These forwarders expose the parse/validate half of the retired
// record layer.

type CapabilityType = capability.CapabilityType

const (
	CapabilityMCP       = capability.CapabilityMCP
	CapabilitySkill     = capability.CapabilitySkill
	CapabilityAPI       = capability.CapabilityAPI
	CapabilityComposite = capability.CapabilityComposite
)

// CapabilityFormatV4 is the format string a v4 document must declare.
const CapabilityFormatV4 = capability.FormatV4

// ParseCapabilityPackage parses and validates one memhop-capability/v4
// package document into its capability cards.
func ParseCapabilityPackage(data []byte, source string) ([]capability.CapabilityImport, error) {
	return capability.BuildPackage(data, source)
}

// ValidateCapabilityCard checks one capability card against the v4 rules.
func ValidateCapabilityCard(card *capability.CapabilityImport) error {
	return capability.ValidateCard(card)
}

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
	RoleDream  = core.RoleDream
)

// ---- L6 trajectory node type / plan status constants ----

const (
	NodeTypeEvent = core.NodeTypeEvent
	NodeTypePlan  = core.NodeTypePlan
)

const (
	StatusPending    = core.StatusPending
	StatusInProgress = core.StatusInProgress
	StatusDone       = core.StatusDone
	StatusFailed     = core.StatusFailed
	StatusRunning    = core.StatusRunning
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

// FormatID renders any record or domain ID as its external 16-char hex form.
// Hex strings are the only id shape the facade exchanges with a host: ids are
// issued by the library (Search mints turn ids, MintPlanID derives plan ids),
// so a host never has to build one from an integer.
func FormatID(id uint64) string { return common.FormatHash(id) }

// ParseID parses a 16-char hex ID.
func ParseID(s string) (uint64, error) { return common.ParseID(s) }

// MintPlanID derives the stable 16-char hex id of the plan a host names. The
// mapping is deterministic, so a restart recovers the same tree without the
// host storing the derived id.
func MintPlanID(name string) string { return plan.MintID(name) }
