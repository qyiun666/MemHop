// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 capability operations of the internal layer: path import / query / lifecycle /
// usage feedback. MemHop stores capabilities; the host executes them from
// the referenced paths or registered MCP tools.

package internal

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

const capabilityFormatV3 = "memhop-capability/v3"

// CapabilityImport is the memhop-capability/v3 JSON file loaded from a path.
// The resource tool-declaration fields (Name/Desc/Input/Output) mirror the
// host tool spec shape so hosts project capabilities with a pure field copy.
type CapabilityImport struct {
	Format    string              `json:"format"`
	Name      string              `json:"name"`
	Version   string              `json:"version,omitempty"`
	Type      core.CapabilityType `json:"type"`
	Summary   string              `json:"summary"`
	Trigger   string              `json:"trigger"`
	Resources []core.ResourceRef  `json:"resources"`
	Workflow  *core.Workflow      `json:"workflow,omitempty"`
}

// CapabilityListQuery filters L5 capabilities.
type CapabilityListQuery struct {
	Status  *core.CapabilityStatus `json:"status,omitempty"`
	Type    *core.CapabilityType   `json:"type,omitempty"`
	Keyword string                 `json:"keyword,omitempty"`
}

// ImportCapability reads a memhop-capability/v3 file (or a directory
// containing capability.json) and upserts it into L5. Repeated imports by
// the same name update the definition while preserving usage statistics.
func (db *DB) ImportCapability(agentID uint64, path string) (*core.Capability, error) {
	data, resolved, err := readCapabilityFile(path)
	if err != nil {
		return nil, err
	}
	return db.importCapabilityData(agentID, data, resolved)
}

// BuildCapability parses and validates one memhop-capability/v3 document
// into an in-memory Capability. It touches no storage: lifecycle fields
// (Status/Origin/timestamps) and IDHash are left to the caller.
func BuildCapability(data []byte, source string) (*core.Capability, error) {
	var in CapabilityImport
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "parse capability import file "+source, err)
	}
	if in.Format != capabilityFormatV3 {
		return nil, common.NewError(common.ErrInvalidQuery,
			"capability file must declare format "+capabilityFormatV3)
	}
	if err := validateCapabilityImport(&in); err != nil {
		return nil, err
	}
	return &core.Capability{
		Name:      in.Name,
		Version:   defaultString(in.Version, "1"),
		Type:      in.Type,
		Summary:   in.Summary,
		Trigger:   in.Trigger,
		Resources: in.Resources,
		Workflow:  in.Workflow,
		FileHash:  sha256Hex(data),
	}, nil
}

// importCapabilityData upserts one imported capability document into L5.
// Re-importing byte-identical content under the same name is a no-op: the
// append-only file must not grow on every startup import.
func (db *DB) importCapabilityData(agentID uint64, data []byte, source string) (*core.Capability, error) {
	ac, err := db.contextFor(agentID)
	if err != nil {
		return nil, err
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	cap, err := BuildCapability(data, source)
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	cap.Status = core.CapabilityActive
	cap.Origin = core.CapabilityOriginImported
	cap.CreatedAt = now
	cap.UpdatedAt = now
	// Byte-identical re-import under the same name: return the stored
	// record without appending, preserving usage stats and timestamps.
	if existing, err := core.ReadCapability(db.engine, agentID, core.CapabilityID(cap.Name)); err == nil &&
		existing.FileHash != "" && existing.FileHash == cap.FileHash {
		return existing, nil
	}
	if _, err := repo.UpsertCapabilityL5(db.engine, agentID, cap); err != nil {
		return nil, err
	}
	return cap, nil
}

// GetCapability reads one L5 capability by ID. IDs not stored in the file
// fall back to the built-in toolbox, so listed built-ins stay retrievable.
func (db *DB) GetCapability(agentID uint64, id string) (*core.Capability, error) {
	ac, err := db.contextFor(agentID)
	if err != nil {
		return nil, err
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	cap, err := repo.GetCapabilityL5(db.engine, agentID, id)
	if err != nil {
		if common.CodeOf(err) == common.ErrNotFound {
			if b := db.findBuiltinCapability(id); b != nil {
				return b, nil
			}
		}
		return nil, err
	}
	return cap, nil
}

// CapabilityPatch is the partial-update payload of UpdateCapability; nil
// fields are left unchanged. Name is immutable: the ID derives from it, so
// renaming means delete + import.
type CapabilityPatch struct {
	Version   *string
	Type      *core.CapabilityType
	Summary   *string
	Trigger   *string
	Status    *core.CapabilityStatus
	Resources *[]core.ResourceRef
	Workflow  *core.Workflow
}

// UpdateCapability partially updates a stored capability (built-ins are
// read-only and rejected).
func (db *DB) UpdateCapability(agentID uint64, id string, patch CapabilityPatch) (*core.Capability, error) {
	ac, err := db.contextFor(agentID)
	if err != nil {
		return nil, err
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	if db.findBuiltinCapability(id) != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "built-in capabilities are read-only")
	}
	cap, err := repo.GetCapabilityL5(db.engine, agentID, id)
	if err != nil {
		return nil, err
	}
	if patch.Version != nil {
		cap.Version = *patch.Version
	}
	if patch.Type != nil {
		cap.Type = *patch.Type
	}
	if patch.Summary != nil {
		cap.Summary = *patch.Summary
	}
	if patch.Trigger != nil {
		cap.Trigger = *patch.Trigger
	}
	if patch.Status != nil {
		cap.Status = *patch.Status
	}
	if patch.Resources != nil {
		cap.Resources = *patch.Resources
	}
	if patch.Workflow != nil {
		cap.Workflow = patch.Workflow
	}
	if err := validateCapabilityImport(&CapabilityImport{
		Name: cap.Name, Version: cap.Version, Type: cap.Type,
		Summary: cap.Summary, Trigger: cap.Trigger,
		Resources: cap.Resources, Workflow: cap.Workflow,
	}); err != nil {
		return nil, err
	}
	// The stored content is no longer the imported bytes.
	cap.FileHash = ""
	if _, err := repo.UpsertCapabilityL5(db.engine, agentID, cap); err != nil {
		return nil, err
	}
	return cap, nil
}

// DeleteCapability removes a capability record. Built-in capabilities are
// read-only: deleting one is rejected instead of silently succeeding.
func (db *DB) DeleteCapability(agentID uint64, id string) error {
	ac, err := db.contextFor(agentID)
	if err != nil {
		return err
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	if _, err := common.ParseID(id); err != nil {
		return common.NewError(common.ErrInvalidQuery, "parse capability id", err)
	}
	if db.findBuiltinCapability(id) != nil {
		return common.NewError(common.ErrInvalidQuery, "built-in capabilities are read-only")
	}
	if !repo.DeleteCapabilityL5(db.engine, agentID, id) {
		return common.NewError(common.ErrIO, "delete capability", nil)
	}
	return nil
}

// ListCapabilities lists and filters L5 capabilities.
func (db *DB) ListCapabilities(agentID uint64, q CapabilityListQuery) ([]core.Capability, error) {
	ac, err := db.contextFor(agentID)
	if err != nil {
		return nil, err
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	kw := strings.ToLower(q.Keyword)
	all := repo.ListCapabilitiesL5(db.engine, agentID)
	filtered := make([]core.Capability, 0, len(all))
	for _, cap := range all {
		if q.Status != nil && cap.Status != *q.Status {
			continue
		}
		if q.Type != nil && cap.Type != *q.Type {
			continue
		}
		if kw != "" && !strings.Contains(strings.ToLower(cap.Name+" "+cap.Summary+" "+cap.Trigger), kw) {
			continue
		}
		filtered = append(filtered, cap)
	}
	// Merge the built-in toolbox through the same filters; a stored record
	// with the same ID wins over its built-in twin. The dedup set is built
	// from ALL stored records (not just the filtered ones) so a stored
	// record filtered out by status/kind still suppresses its built-in twin.
	stored := make(map[uint64]struct{}, len(all))
	for _, cap := range all {
		stored[cap.IDHash] = struct{}{}
	}
	filtered = append(filtered, db.builtinMatchingList(q, kw, stored)...)
	slices.SortFunc(filtered, func(a, b core.Capability) int {
		return cmp.Compare(b.UpdatedAt, a.UpdatedAt)
	})
	if filtered == nil {
		return []core.Capability{}, nil
	}
	return filtered, nil
}

// ActivateCapability promotes a draft capability to active. Built-in
// capabilities are read-only and rejected.
func (db *DB) ActivateCapability(agentID uint64, id string) (*core.Capability, error) {
	ac, err := db.contextFor(agentID)
	if err != nil {
		return nil, err
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	if db.findBuiltinCapability(id) != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "built-in capabilities are read-only")
	}
	return repo.ActivateCapabilityL5(db.engine, agentID, id)
}

// RecordCapabilityUsage records host feedback after a capability was used.
// Built-in capabilities are read-only and rejected.
func (db *DB) RecordCapabilityUsage(agentID uint64, id string, success bool) (*core.Capability, error) {
	ac, err := db.contextFor(agentID)
	if err != nil {
		return nil, err
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	if db.findBuiltinCapability(id) != nil {
		return nil, common.NewError(common.ErrInvalidQuery, "built-in capabilities are read-only")
	}
	return repo.RecordCapabilityUsageL5(db.engine, agentID, id, success)
}

// RenderCapabilityPrompt renders capabilities as compact prompt cards for an
// LLM context.
func RenderCapabilityPrompt(caps []core.Capability) string {
	if len(caps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Available capabilities:\n")
	for i := range caps {
		b.WriteString(caps[i].PromptCard())
		b.WriteByte('\n')
	}
	return b.String()
}

func readCapabilityFile(path string) ([]byte, string, error) {
	if path == "" {
		return nil, "", common.NewError(common.ErrInvalidQuery, "capability path is required")
	}
	resolved := path
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", common.NewError(common.ErrIO, "stat capability path", err)
	}
	if info.IsDir() {
		resolved = filepath.Join(path, "capability.json")
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, "", common.NewError(common.ErrIO, "read capability file", err)
	}
	return data, resolved, nil
}

func validateCapabilityImport(in *CapabilityImport) error {
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

// matchCapabilities matches stored capabilities against text. Search stays
// pure retrieval: the built-in toolbox is never attached to Search
// responses.
func (db *DB) matchCapabilities(text string) []core.Capability {
	return repo.MatchCapabilitiesL5(db.engine, core.DefaultAgentID, text)
}
