// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package capability is the L5 capability-definition capability: parsing,
// validation, projection and filtering of memhop-capability/v4 package
// documents. It is stateless and identity-neutral — it receives documents and
// returns records/verdicts; storage reads/writes and the domain lock stay in
// the composition root.
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

// FormatV4 is the only supported capability document format: a plugin
// package holding one or more capability cards.
const FormatV4 = "memhop-capability/v4"

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

// BuildPackage parses and validates one memhop-capability/v4 package document
// into its capability cards, each stamped with the package name. It touches no
// storage: lifecycle fields (Status/Origin/timestamps) and IDHash are left to
// the caller. FileHash is per card the hash of the whole document, so a
// byte-identical re-import of an unchanged package is detectable per card.
func BuildPackage(data []byte, source string) ([]*core.Capability, error) {
	var in core.CapabilityPackageDoc
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse capability package file "+source, err)
	}
	if in.Format != FormatV4 {
		return nil, common.NewError(common.ErrInvalidQuery,
			"capability file must declare format "+FormatV4)
	}
	if err := ValidatePackage(&in); err != nil {
		return nil, err
	}
	hash := sha256Hex(data)
	caps := make([]*core.Capability, 0, len(in.Capabilities))
	for i := range in.Capabilities {
		cap := FromImport(&in.Capabilities[i])
		cap.Package = in.Name
		cap.FileHash = hash
		caps = append(caps, cap)
	}
	return caps, nil
}

// ValidatePackage checks a parsed package document: package name required,
// 1..N cards, card names unique within the package (the card ID derives from
// the name, so a duplicate would silently alias one record), and each card
// validated.
func ValidatePackage(in *core.CapabilityPackageDoc) error {
	if strings.TrimSpace(in.Name) == "" {
		return common.NewError(common.ErrInvalidQuery, "capability package name is required")
	}
	if len(in.Capabilities) == 0 {
		return common.NewError(common.ErrInvalidQuery, "capability package requires at least one capability")
	}
	seen := make(map[string]struct{}, len(in.Capabilities))
	for i := range in.Capabilities {
		if err := ValidateCard(&in.Capabilities[i]); err != nil {
			return err
		}
		key := core.NormalizeCapabilityName(in.Capabilities[i].Name)
		if _, dup := seen[key]; dup {
			return common.NewError(common.ErrInvalidQuery, "duplicate capability name in package: "+in.Capabilities[i].Name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// ValidateCard checks one capability card: name, trigger/summary presence,
// the resource shape and JSON-Schema-shaped tool declarations. One card
// carries any number of function entries — there is no card-level type.
func ValidateCard(in *core.CapabilityImport) error {
	if strings.TrimSpace(in.Name) == "" {
		return common.NewError(common.ErrInvalidQuery, "capability name is required")
	}
	if strings.TrimSpace(in.Trigger) == "" && strings.TrimSpace(in.Summary) == "" {
		return common.NewError(common.ErrInvalidQuery, "capability trigger or summary is required")
	}
	if len(in.Resources) == 0 {
		return common.NewError(common.ErrInvalidQuery, "capability requires at least one resource entry")
	}
	for _, res := range in.Resources {
		if strings.TrimSpace(res.Name) == "" {
			return common.NewError(common.ErrInvalidQuery, "resource name is required")
		}
		switch res.Type {
		case core.CapabilityMCP, core.CapabilitySkill, core.CapabilityAPI, core.CapabilityComposite:
		default:
			return common.NewError(common.ErrInvalidQuery,
				"resource type must be one of mcp|skill|api|composite: "+res.Name)
		}
		if strings.TrimSpace(res.Input) != "" && !json.Valid([]byte(res.Input)) {
			return common.NewError(common.ErrInvalidQuery,
				"resource input must be valid JSON: "+res.Name)
		}
		if err := validateConfig(res.Config, res.Name); err != nil {
			return err
		}
	}
	return nil
}

// validateConfig checks a resource Config: a JSON-shaped value ({/[ prefix)
// must parse, and an object carrying a canonical "steps" action chain must
// have every step name its tool — the shape hosts replay. Loose line forms
// and natural-language configs are the host's to parse and pass unchecked.
func validateConfig(cfg *string, resName string) error {
	if cfg == nil {
		return nil
	}
	c := *cfg
	if !strings.HasPrefix(c, "{") && !strings.HasPrefix(c, "[") {
		return nil
	}
	if !json.Valid([]byte(c)) {
		return common.NewError(common.ErrInvalidQuery, "resource config must be valid JSON: "+resName)
	}
	var obj struct {
		Steps []json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal([]byte(c), &obj); err != nil || len(obj.Steps) == 0 {
		return nil // not a steps object: other JSON payloads are opaque
	}
	for _, raw := range obj.Steps {
		var st struct {
			Tool string `json:"tool"`
		}
		if err := json.Unmarshal(raw, &st); err != nil || strings.TrimSpace(st.Tool) == "" {
			return common.NewError(common.ErrInvalidQuery,
				`resource config step must carry a non-empty "tool" key: `+resName)
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
		Summary:   in.Summary,
		Trigger:   in.Trigger,
		Resources: in.Resources,
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
// with the incoming ones (usage statistics and identity are preserved; the
// package stamp is immutable). The caller persists the result.
func MergeDefinition(existing, incoming *core.Capability, now int64) {
	existing.Version = incoming.Version
	existing.Summary = incoming.Summary
	existing.Trigger = incoming.Trigger
	existing.Resources = incoming.Resources
	existing.UpdatedAt = now
}

// Matches is the list-filter predicate shared by stored and built-in
// capabilities: nil Status/Package filters pass everything, a non-empty
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
	if q.Package != nil && cap.Package != *q.Package {
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
