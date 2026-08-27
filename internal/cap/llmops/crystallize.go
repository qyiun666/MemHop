// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// llm_crystallize.go: L6→L5 capability crystallization call point — the
// LLM extracts reusable capability cards from an operation trajectory and
// compares them against the existing catalog (create / reuse / merge).

package llmops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// CrystallizeCapability is one capability candidate extracted from a
// trajectory. Action is create, reuse or merge.
type CrystallizeCapability struct {
	Action     string                `json:"action"`
	ReuseID    string                `json:"reuse_id,omitempty"`
	Capability core.CapabilityImport `json:"capability"`
}

type CrystallizeOutput struct {
	Capabilities []CrystallizeCapability `json:"capabilities"`
}

const systemCrystallize = `You analyze an agent's operation trajectory and extract reusable L5 capabilities.

Rules:
- Only extract capabilities that are clearly reusable (appear at least twice or are obviously generic procedures)
- A capability has type skill, mcp, api, or composite:
  * skill: a reusable skill/runbook/SOP (ref = skill path or manual reference)
  * mcp: a single reusable tool provided by an MCP server (ref = server address)
  * api: a single reusable host method (ref = "api:MethodName")
  * composite: an orchestration of the above resources, with an optional workflow
- For composite capabilities, list the referenced resources in "resources" and their ordered orchestration in workflow.steps (ref refers to a resources[].name; args carries step parameters). Do not invent tools or services that are not present in the trajectory
- Every resource is a tool declaration: name = tool name, desc = how to call it (for the LLM), input = args JSON Schema string (omit when none), output = output description, ref = server address / skill path / api:Method, config = connection JSON (optional)
- Compare against the existing capabilities listed below. If the same capability already exists:
  * action = "reuse" and reuse_id = its 16-hex id
  * do not duplicate it
- If a candidate is a newer variant of an existing capability, use action = "merge" and reuse_id = its existing id
- Otherwise action = "create"
- When no reusable capability exists, output capabilities as an empty array

Output ONLY valid JSON in this exact shape (no markdown, no code fences):
{
  "capabilities": [
    {
      "action": "create|reuse|merge",
      "reuse_id": "16-hex-id when action is reuse or merge, otherwise omit",
      "capability": {
        "name": "<short capability name>",
        "version": "1",
        "type": "skill|mcp|api|composite",
        "summary": "<one sentence>",
        "trigger": "<when this capability applies>",
        "resources": [
          {"type": "skill|mcp|api", "name": "<tool name>", "desc": "<how to call it, for the LLM>", "input": "<args JSON Schema string, omit when none>", "output": "<output description>", "ref": "<server address / skill path / api:Method>", "config": "<connection JSON, optional>"}
        ],
        "workflow": {"steps": [{"ref": "<resources[].name>", "action": "<what this step does>", "args": {"<param>": "<value>"}}]}
      }
    }
  ]
}`

// Crystallize extracts reusable L5 capabilities from a trajectory event
// batch. Existing capabilities are included in the prompt so the model can
// reuse or merge instead of duplicating.
func Crystallize(ctx context.Context, chat Chat, events []core.TrajectorySlot, existing []core.Capability) (*CrystallizeOutput, error) {
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

// buildCrystallizePrompt lists trajectory events followed by existing L5
// capability prompt cards.
func buildCrystallizePrompt(events []core.TrajectorySlot, existing []core.Capability) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Operation Trajectory (%d events)\n\n", len(events))
	for _, ev := range events {
		fmt.Fprintf(&b, "[seq=%d type=%s] %s\n", ev.Seq, ev.EventType, ev.Payload)
	}
	b.WriteString("\n# Existing L5 capabilities\n")
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
			Action     string                `json:"action"`
			ReuseID    string                `json:"reuse_id,omitempty"`
			Capability core.CapabilityImport `json:"capability"`
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
