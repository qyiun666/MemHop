// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// crystallize.go: capability crystallization call point — the
// LLM extracts reusable capability cards from an operation trajectory and
// compares them against the existing catalog (create / reuse / merge).

package llmops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qyiun666/MemHop/internal/cap/capability"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// CrystallizeCapability is one capability candidate extracted from a
// trajectory. Action is create, reuse or merge; ReuseID names the existing
// capability (its exact name) for reuse/merge — the host resolves it against
// its own directory.
type CrystallizeCapability struct {
	Action     string                      `json:"action"`
	ReuseID    string                      `json:"reuse_id,omitempty"`
	Capability capability.CapabilityImport `json:"capability"`
}

type CrystallizeOutput struct {
	Capabilities []CrystallizeCapability `json:"capabilities"`
}

const systemCrystallize = `You analyze an agent's operation trajectory and extract reusable capabilities.

Rules:
- Only extract capabilities that are clearly reusable (appear at least twice or are obviously generic procedures)
- A capability is a named card of function entries ("resources"); there is no card-level type. Each entry declares how it is launched:
  * skill: a reusable skill/runbook/SOP (type = "skill", ref = skill path or manual reference)
  * mcp: a tool provided by an MCP server (type = "mcp", ref = server address)
  * api: a host method (type = "api", ref = "api:MethodName")
  * composite: an ordered action chain over other entries or host tools (type = "composite", config = the step chain {"steps":[{"tool":"<entry name or host tool name>","args":{...}}]})
- Every resource is a tool declaration: name = tool name, desc = how to call it (for the LLM), input = args JSON Schema string (omit when none), output = output description, ref = server address / skill path / api:Method, config = connection JSON, or for composite the step chain (optional)
- Do not invent tools or services that are not present in the trajectory
- Compare against the existing capabilities listed below. If the same capability already exists:
  * action = "reuse" and reuse_id = its exact name
  * do not duplicate it
- If a candidate is a newer variant of an existing capability, use action = "merge" and reuse_id = the existing capability's name
- Otherwise action = "create"
- When no reusable capability exists, output capabilities as an empty array

Output ONLY valid JSON in this exact shape (no markdown, no code fences):
{
  "capabilities": [
    {
      "action": "create|reuse|merge",
      "reuse_id": "existing capability name when action is reuse or merge, otherwise omit",
      "capability": {
        "name": "<short capability name>",
        "version": "1",
        "summary": "<one sentence>",
        "trigger": "<when this capability applies>",
        "resources": [
          {"type": "skill|mcp|api|composite", "name": "<tool name>", "desc": "<how to call it, for the LLM>", "input": "<args JSON Schema string, omit when none>", "output": "<output description>", "ref": "<server address / skill path / api:Method>", "config": "<connection JSON; for composite the chain {\"steps\":[{\"tool\":\"...\",\"args\":{...}}]}>"}
        ]
      }
    }
  ]
}`

// Crystallize extracts reusable capabilities from a trajectory event
// batch. Existing capabilities (the host's own catalog) are included in the
// prompt so the model can reuse or merge instead of duplicating.
func Crystallize(ctx context.Context, chat Chat, events []core.ArchiveSlot, existing []capability.CapabilityImport) (*CrystallizeOutput, error) {
	if len(events) == 0 {
		return &CrystallizeOutput{Capabilities: []CrystallizeCapability{}}, nil
	}
	user := buildCrystallizePrompt(events, existing)
	response, err := chat.Chat(ctx, systemCrystallize, user, chat.MaxOutputTokens(), 0.0, 1.0)
	if err != nil {
		return nil, err
	}
	return parseCrystallizeResponse(response)
}

// buildCrystallizePrompt lists trajectory events followed by the host's
// existing capability prompt cards.
func buildCrystallizePrompt(events []core.ArchiveSlot, existing []capability.CapabilityImport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Operation Trajectory (%d events)\n\n", len(events))
	for _, ev := range events {
		fmt.Fprintf(&b, "[seq=%d type=%s] %s\n", ev.Seq, ev.EventType, ev.Content)
	}
	b.WriteString("\n# Existing capabilities\n")
	if len(existing) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, cap := range existing {
			b.WriteString(cap.PromptCard())
			b.WriteByte('\n')
		}
	}
	b.WriteString("\nExtract reusable capabilities now.")
	return b.String()
}

// parseCrystallizeResponse parses the LLM reply, dropping malformed rows.
func parseCrystallizeResponse(response string) (*CrystallizeOutput, error) {
	cleaned := stripCodeBlocks(response)
	var raw struct {
		Capabilities []struct {
			Action     string                      `json:"action"`
			ReuseID    string                      `json:"reuse_id,omitempty"`
			Capability capability.CapabilityImport `json:"capability"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(cleaned), &raw); err != nil {
		return nil, common.NewError(common.ErrLLM, "crystallize response parse failed", err)
	}
	out := &CrystallizeOutput{Capabilities: make([]CrystallizeCapability, 0, len(raw.Capabilities))}
	for _, c := range raw.Capabilities {
		if strings.TrimSpace(c.Capability.Name) == "" {
			continue
		}
		action := strings.ToLower(strings.TrimSpace(c.Action))
		if action == "" {
			action = "create"
		}
		if action != "create" && action != "reuse" && action != "merge" {
			continue
		}
		out.Capabilities = append(out.Capabilities, CrystallizeCapability{
			Action: action, ReuseID: c.ReuseID, Capability: c.Capability,
		})
	}
	return out, nil
}
