// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package capability is the L5 capability-definition capability: the
// memhop-capability/v4 document types with their parsing, validation and
// prompt rendering. It is stateless and identity-neutral. The engine stores
// no capability records — a host owns its capability directory, scans it and
// reuses this parser as the disk format's single source of truth.
package capability

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
)

// FormatV4 is the only supported capability document format: a plugin
// package holding one or more capability cards.
const FormatV4 = "memhop-capability/v4"

// CapabilityType describes how a capability resource is implemented: a wrapper
// around a single MCP tool, a single skill, or a composite of several
// resources.
type CapabilityType string

const (
	CapabilityMCP   CapabilityType = "mcp"
	CapabilitySkill CapabilityType = "skill"
	// CapabilityAPI wraps one method of the MemHop Go API (api package);
	// the host calls it directly through the library facade.
	CapabilityAPI       CapabilityType = "api"
	CapabilityComposite CapabilityType = "composite"
)

// ResourceRef is one function entry of a capability card (an MCP server, a
// skill, an api method, or an action chain). The tool-declaration fields
// (Name/Desc/Input/Output) mirror the host tool spec shape exactly (meowire
// ToolSpec semantics): a host projects a resource to its own tool declaration
// with a pure field copy, no format conversion. MemHop never executes these
// references — launching them is the host's job.
type ResourceRef struct {
	Type   CapabilityType `json:"type"`             // mcp | skill | api | composite
	Name   string         `json:"name"`             // tool name (ToolSpec.Name)
	Desc   string         `json:"desc"`             // call contract for the LLM (ToolSpec.Desc)
	Input  string         `json:"input,omitempty"`  // args JSON Schema string (ToolSpec.Input)
	Output string         `json:"output,omitempty"` // output description (ToolSpec.Output)
	Ref    string         `json:"ref,omitempty"`    // MCP server address / skill path / api:Method / command
	Config *string        `json:"config,omitempty"` // connection config; a composite entry carries its action chain as {"steps":[{"tool":...}]}
}

// CapabilityImport is one capability card inside a memhop-capability/v4
// package document. The resource tool-declaration fields (Name/Desc/Input/
// Output) mirror the host tool spec shape so hosts project capabilities with
// a pure field copy.
type CapabilityImport struct {
	Name      string        `json:"name"`
	Version   string        `json:"version,omitempty"`
	Summary   string        `json:"summary"`
	Trigger   string        `json:"trigger"`
	Resources []ResourceRef `json:"resources"`
}

// CapabilityPackageDoc is the memhop-capability/v4 JSON document: a plugin
// package holding one or more capability cards. A single-card file is just a
// package with one entry.
type CapabilityPackageDoc struct {
	Format       string             `json:"format"`
	Name         string             `json:"name"`
	Capabilities []CapabilityImport `json:"capabilities"`
}

// NormalizeCapabilityName returns the canonical lowercase name used for
// duplicate detection within a package.
func NormalizeCapabilityName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// BuildPackage parses and validates one memhop-capability/v4 package
// document into its capability cards. It touches no storage and no
// filesystem: the caller reads the file and owns what happens to the cards.
func BuildPackage(data []byte, source string) ([]CapabilityImport, error) {
	var in CapabilityPackageDoc
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
	return in.Capabilities, nil
}

// ValidatePackage checks a parsed package document: package name required,
// 1..N cards, card names unique within the package (the name is the card's
// addressable identity, so a duplicate would silently alias one card), and
// each card validated.
func ValidatePackage(in *CapabilityPackageDoc) error {
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
		key := NormalizeCapabilityName(in.Capabilities[i].Name)
		if _, dup := seen[key]; dup {
			return common.NewError(common.ErrInvalidQuery, "duplicate capability name in package: "+in.Capabilities[i].Name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// ValidateCard checks one capability card: name, trigger/summary presence,
// the resource shape (non-empty names, a type from the four-value enum,
// JSON-valid input declarations and step chains). One card carries any
// number of function entries — there is no card-level type.
func ValidateCard(in *CapabilityImport) error {
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
		case CapabilityMCP, CapabilitySkill, CapabilityAPI, CapabilityComposite:
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

// PromptCard renders the concise capability view intended for an LLM prompt:
// name, version, summary and trigger, then one block per resource with its
// call contract. A card's addressable identity is its name — there is no
// stored id to render.
func (c *CapabilityImport) PromptCard() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[capability: %s]\n", c.Name)
	if c.Version != "" {
		fmt.Fprintf(&b, "version: %s\n", c.Version)
	}
	if c.Summary != "" {
		fmt.Fprintf(&b, "summary: %s\n", c.Summary)
	}
	if c.Trigger != "" {
		fmt.Fprintf(&b, "trigger: %s\n", c.Trigger)
	}
	for _, r := range c.Resources {
		fmt.Fprintf(&b, "resource: %s %s", r.Type, r.Name)
		if r.Ref != "" {
			fmt.Fprintf(&b, " (%s)", r.Ref)
		}
		b.WriteByte('\n')
		if r.Desc != "" {
			fmt.Fprintf(&b, "  use: %s\n", r.Desc)
		}
		if r.Input != "" {
			fmt.Fprintf(&b, "  input: %s\n", r.Input)
		}
		if r.Output != "" {
			fmt.Fprintf(&b, "  output: %s\n", r.Output)
		}
		if steps := resourceSteps(r.Config); len(steps) > 0 {
			fmt.Fprintf(&b, "  steps: %s\n", strings.Join(steps, " -> "))
		}
	}
	return b.String()
}

// resourceSteps extracts the action-chain tool names from a resource Config
// of the canonical form {"steps":[{"tool":"...", ...}]}; other shapes (loose
// line forms, natural language) yield nil. Rendering only — shape validation
// lives in ValidateCard at parse time.
func resourceSteps(cfg *string) []string {
	if cfg == nil || !strings.HasPrefix(*cfg, "{") {
		return nil
	}
	var obj struct {
		Steps []struct {
			Tool string `json:"tool"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(*cfg), &obj); err != nil || len(obj.Steps) == 0 {
		return nil
	}
	names := make([]string, 0, len(obj.Steps))
	for _, st := range obj.Steps {
		if st.Tool != "" {
			names = append(names, st.Tool)
		}
	}
	return names
}
