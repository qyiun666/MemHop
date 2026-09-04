// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package capability is the L5 capability-definition capability: parsing,
// validation, projection and filtering of memhop-capability/v3 documents.
// It is stateless and identity-neutral — it receives documents and returns
// records/verdicts; storage reads/writes and the domain lock stay in the
// composition root.
package capability

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// FormatV3 is the only supported capability document format.
const FormatV3 = "memhop-capability/v3"

// ReadFile loads a capability document from a path (a directory resolves to
// its capability.json).
func ReadFile(path string) (data []byte, resolved string, err error) {
	if path == "" {
		return nil, "", common.NewError(common.ErrInvalidQuery, "capability path is required")
	}
	resolved = path
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", common.NewError(common.ErrIO, "stat capability path", err)
	}
	if info.IsDir() {
		resolved = filepath.Join(path, "capability.json")
	}
	data, err = os.ReadFile(resolved)
	if err != nil {
		return nil, "", common.NewError(common.ErrIO, "read capability file", err)
	}
	return data, resolved, nil
}

// Build parses and validates one memhop-capability/v3 document into an
// in-memory Capability. It touches no storage: lifecycle fields
// (Status/Origin/timestamps) and IDHash are left to the caller.
func Build(data []byte, source string) (*core.Capability, error) {
	var in core.CapabilityImport
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse capability import file "+source, err)
	}
	if in.Format != FormatV3 {
		return nil, common.NewError(common.ErrInvalidQuery,
			"capability file must declare format "+FormatV3)
	}
	if err := Validate(&in); err != nil {
		return nil, err
	}
	cap := FromImport(&in)
	cap.FileHash = sha256Hex(data)
	return cap, nil
}

// Validate checks a parsed import document: name, trigger/summary presence,
// the type-dependent resource shape and JSON-Schema-shaped tool declarations.
func Validate(in *core.CapabilityImport) error {
	if strings.TrimSpace(in.Name) == "" {
		return common.NewError(common.ErrInvalidQuery, "capability name is required")
	}
	if strings.TrimSpace(in.Trigger) == "" && strings.TrimSpace(in.Summary) == "" {
		return common.NewError(common.ErrInvalidQuery, "capability trigger or summary is required")
	}
	switch in.Type {
	case core.CapabilityMCP, core.CapabilitySkill, core.CapabilityAPI:
		if len(in.Resources) != 1 || in.Resources[0].Type != in.Type {
			return common.NewError(common.ErrInvalidQuery,
				"capability of type "+string(in.Type)+" requires exactly one resource of the same type")
		}
	case core.CapabilityComposite:
		if len(in.Resources) == 0 {
			return common.NewError(common.ErrInvalidQuery, "composite capability requires at least one resource")
		}
		if in.Workflow != nil {
			for _, step := range in.Workflow.Steps {
				if strings.TrimSpace(step.Ref) == "" {
					return common.NewError(common.ErrInvalidQuery, "workflow step ref is required")
				}
			}
		}
	case "":
		return common.NewError(common.ErrInvalidQuery, "capability type is required")
	default:
		return common.NewError(common.ErrInvalidQuery, "unknown capability type: "+string(in.Type))
	}
	for _, res := range in.Resources {
		if strings.TrimSpace(res.Name) == "" {
			return common.NewError(common.ErrInvalidQuery, "resource name is required")
		}
		if strings.TrimSpace(res.Input) != "" && !json.Valid([]byte(res.Input)) {
			return common.NewError(common.ErrInvalidQuery,
				"resource input must be a valid JSON Schema string: "+res.Name)
		}
	}
	return nil
}

// FromImport copies the definition fields of an import document into a fresh
// Capability; lifecycle fields, IDHash and FileHash are left to the caller
// (import / crystallize set them differently).
func FromImport(in *core.CapabilityImport) *core.Capability {
	return &core.Capability{
		Name:      in.Name,
		Version:   defaultString(in.Version, "1"),
		Type:      in.Type,
		Summary:   in.Summary,
		Trigger:   in.Trigger,
		Resources: in.Resources,
		Workflow:  in.Workflow,
	}
}

// BuildCrystallized assembles the draft capability record from a crystallize
// candidate: the shared definition copy plus crystallization lifecycle fields.
func BuildCrystallized(in *core.CapabilityImport, now int64) *core.Capability {
	cap := FromImport(in)
	cap.Status = core.CapabilityDraft
	cap.Origin = core.CapabilityOriginCrystallized
	cap.CreatedAt = now
	cap.UpdatedAt = now
	return cap
}

// MergeDefinition overwrites the definition fields of an existing capability
// with the incoming ones (usage statistics and identity are preserved). The
// caller persists the result.
func MergeDefinition(existing, incoming *core.Capability, now int64) {
	existing.Version = incoming.Version
	existing.Type = incoming.Type
	existing.Summary = incoming.Summary
	existing.Trigger = incoming.Trigger
	existing.Resources = incoming.Resources
	existing.Workflow = incoming.Workflow
	existing.UpdatedAt = now
}

// Matches is the list-filter predicate shared by stored and built-in
// capabilities: nil Status/Type filters pass everything, a non-empty
// lowercased keyword must appear in name+summary+trigger, and a non-empty IDs
// set restricts the result to those 16-hex ids (a malformed id matches
// nothing).
func Matches(cap *core.Capability, q *core.CapabilityListQuery, kw string) bool {
	if len(q.IDs) > 0 && !matchesID(cap.IDHash, q.IDs) {
		return false
	}
	if q.Status != nil && cap.Status != *q.Status {
		return false
	}
	if q.Type != nil && cap.Type != *q.Type {
		return false
	}
	if kw != "" && !strings.Contains(strings.ToLower(cap.Name+" "+cap.Summary+" "+cap.Trigger), kw) {
		return false
	}
	return true
}

func matchesID(idHash uint64, ids []string) bool {
	for _, id := range ids {
		if h, err := common.ParseID(id); err == nil && h == idHash {
			return true
		}
	}
	return false
}

// ActiveOnly keeps the active capabilities of caps (order preserved); used
// by crystallization so the LLM catalog lists only usable cards.
func ActiveOnly(caps []core.Capability) []core.Capability {
	return slices.DeleteFunc(caps, func(c core.Capability) bool {
		return c.Status != core.CapabilityActive
	})
}

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
