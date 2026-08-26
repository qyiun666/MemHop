// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 capability tools: import/get/delete/list/activate/usage/update.
//
// CapabilityPatch carries nested enum and struct fields that the api
// package does not re-export by name; string params are mapped to the api
// enum constants (typed by inference) and the nested resources/workflow
// payloads are round-tripped through JSON, whose field names match the core
// DTOs exactly.

package main

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

type capabilityImportArgs struct {
	Path string `json:"path"`
}

type capabilityIDArgs struct {
	ID string `json:"id"`
}

type capabilityUsageArgs struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
}

type capabilityListArgs struct {
	Status  string `json:"status,omitempty"`
	Type    string `json:"type,omitempty"`
	Keyword string `json:"keyword,omitempty"`
}

// capabilityUpdateArgs carries the partial update fields of
// memhop_capability_update; empty strings mean "leave unchanged".
type capabilityUpdateArgs struct {
	ID        string        `json:"id"`
	Version   string        `json:"version,omitempty"`
	Type      string        `json:"type,omitempty"`
	Summary   string        `json:"summary,omitempty"`
	Trigger   string        `json:"trigger,omitempty"`
	Status    string        `json:"status,omitempty"`
	Resources []resourceArg `json:"resources,omitempty"`
	Workflow  *workflowArg  `json:"workflow,omitempty"`
}

type resourceArg struct {
	Type        string  `json:"type"`
	Name        string  `json:"name"`
	Ref         string  `json:"ref,omitempty"`
	Description string  `json:"description,omitempty"`
	Config      *string `json:"config,omitempty"`
}

type workflowArg struct {
	Steps []workflowStepArg `json:"steps"`
}

type workflowStepArg struct {
	Ref    string `json:"ref"`
	Action string `json:"action,omitempty"`
}

// validCapabilityStatus validates a status string before the typed switch.
func validCapabilityStatus(s string) error {
	switch s {
	case "draft", "active", "deprecated":
		return nil
	}
	return fmt.Errorf("invalid capability status %q (want draft, active or deprecated)", s)
}

// validCapabilityType validates a type string before the typed switch.
func validCapabilityType(s string) error {
	switch s {
	case "mcp", "skill", "api", "composite":
		return nil
	}
	return fmt.Errorf("invalid capability type %q (want mcp, skill, api or composite)", s)
}

// resourceArrayProp is the JSON Schema for a []ResourceRef.
func resourceArrayProp(desc string) map[string]any {
	return map[string]any{
		"type": "array",
		"items": objSchema(map[string]any{
			"type":        strProp("mcp 或 skill"),
			"name":        strProp("mcp 工具名 / skill 名"),
			"ref":         strProp("mcp server 地址 / skill 路径 / 命令"),
			"description": strProp("怎么调用（给 LLM）"),
			"config":      strProp("可选配置（JSON）"),
		}),
		"description": desc,
	}
}

// workflowProp is the JSON Schema for a *Workflow.
func workflowProp() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"steps": map[string]any{
				"type": "array",
				"items": objSchema(map[string]any{
					"ref":    strProp("资源名（Resources[].Name）或另一能力名"),
					"action": strProp("动作说明"),
				}),
				"description": "有序编排步骤",
			},
		},
		"description": "composite 能力的编排（可选）",
	}
}

func registerL5Tools(s *mcp.Server, db *memhop.DB) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_capability_import",
		Description: "导入 memhop-capability/v2 能力文件（文件或包含 capability.json 的目录）。能力是对宿主资源的封装：type=mcp（单个 mcp 工具）、type=skill（单个 skill）、type=api（单个 api 方法）、type=composite（多个 mcp/skill/api 集合，可选 workflow 编排）。",
		InputSchema: objSchema(map[string]any{
			"path": strProp("能力文件或目录路径，必填"),
		}, "path"),
	}, handle[capabilityImportArgs, memhop.Capability](func(a capabilityImportArgs) (memhop.Capability, error) {
		cap, err := db.ImportCapability(a.Path)
		if err != nil {
			return memhop.Capability{}, err
		}
		return *cap, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_capability_get",
		Description: "按 ID 读取一个 L5 能力（含内置能力卡）。",
		InputSchema: objSchema(map[string]any{
			"id": strProp("能力 ID（16 位 hex），必填"),
		}, "id"),
	}, handle[capabilityIDArgs, memhop.Capability](func(a capabilityIDArgs) (memhop.Capability, error) {
		cap, err := db.GetCapability(a.ID)
		if err != nil {
			return memhop.Capability{}, err
		}
		return *cap, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_capability_delete",
		Description: "删除一个 L5 能力（内置能力卡只读，删除会被拒绝）。",
		InputSchema: objSchema(map[string]any{
			"id": strProp("能力 ID（16 位 hex），必填"),
		}, "id"),
	}, handle[capabilityIDArgs, updateResult](func(a capabilityIDArgs) (updateResult, error) {
		return updateResult{OK: true}, db.DeleteCapability(a.ID)
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_capability_list",
		Description: "列出 L5 能力（含内置能力卡）。可按状态（draft/active/deprecated）、类型（mcp/skill/api/composite）与关键词过滤。",
		InputSchema: objSchema(map[string]any{
			"status":  strProp("状态过滤：draft | active | deprecated"),
			"type":    strProp("类型过滤：mcp | skill | api | composite"),
			"keyword": strProp("名称关键词过滤"),
		}),
	}, handle[capabilityListArgs, []memhop.Capability](func(a capabilityListArgs) ([]memhop.Capability, error) {
		var q memhop.CapabilityListQuery
		if a.Status != "" {
			if err := validCapabilityStatus(a.Status); err != nil {
				return nil, err
			}
			// Seed from the api constant: the inferred variable already has
			// the CapabilityStatus type, so &st matches the query field.
			var st = memhop.CapabilityDraft
			switch a.Status {
			case "active":
				st = memhop.CapabilityActive
			case "deprecated":
				st = memhop.CapabilityDeprecated
			}
			q.Status = &st
		}
		if a.Type != "" {
			if err := validCapabilityType(a.Type); err != nil {
				return nil, err
			}
			var typ = memhop.CapabilityMCP
			switch a.Type {
			case "skill":
				typ = memhop.CapabilitySkill
			case "api":
				typ = memhop.CapabilityAPI
			case "composite":
				typ = memhop.CapabilityComposite
			}
			q.Type = &typ
		}
		q.Keyword = a.Keyword
		return db.ListCapabilities(q)
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_capability_activate",
		Description: "激活一个 draft 能力（draft → active）。",
		InputSchema: objSchema(map[string]any{
			"id": strProp("能力 ID（16 位 hex），必填"),
		}, "id"),
	}, handle[capabilityIDArgs, memhop.Capability](func(a capabilityIDArgs) (memhop.Capability, error) {
		cap, err := db.ActivateCapability(a.ID)
		if err != nil {
			return memhop.Capability{}, err
		}
		return *cap, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_capability_usage",
		Description: "记录一次能力调用结果（成功/失败），更新成功率与触发计数。",
		InputSchema: objSchema(map[string]any{
			"id":      strProp("能力 ID（16 位 hex），必填"),
			"success": boolProp("调用是否成功，必填"),
		}, "id", "success"),
	}, handle[capabilityUsageArgs, memhop.Capability](func(a capabilityUsageArgs) (memhop.Capability, error) {
		cap, err := db.RecordCapabilityUsage(a.ID, a.Success)
		if err != nil {
			return memhop.Capability{}, err
		}
		return *cap, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_capability_update",
		Description: "部分更新一个 L5 能力（空字符串字段表示不修改；内置能力卡只读，更新会被拒绝）。",
		InputSchema: objSchema(map[string]any{
			"id":        strProp("能力 ID（16 位 hex），必填"),
			"version":   strProp("版本号"),
			"type":      strProp("类型：mcp | skill | api | composite"),
			"summary":   strProp("能力摘要"),
			"trigger":   strProp("触发条件描述"),
			"status":    strProp("状态：draft | active | deprecated"),
			"resources": resourceArrayProp("资源列表"),
			"workflow":  workflowProp(),
		}, "id"),
	}, handle[capabilityUpdateArgs, memhop.Capability](func(a capabilityUpdateArgs) (memhop.Capability, error) {
		var patch memhop.CapabilityPatch
		if a.Version != "" {
			patch.Version = &a.Version
		}
		if a.Summary != "" {
			patch.Summary = &a.Summary
		}
		if a.Trigger != "" {
			patch.Trigger = &a.Trigger
		}
		if a.Type != "" {
			if err := validCapabilityType(a.Type); err != nil {
				return memhop.Capability{}, err
			}
			var typ = memhop.CapabilityMCP
			switch a.Type {
			case "skill":
				typ = memhop.CapabilitySkill
			case "api":
				typ = memhop.CapabilityAPI
			case "composite":
				typ = memhop.CapabilityComposite
			}
			patch.Type = &typ
		}
		if a.Status != "" {
			if err := validCapabilityStatus(a.Status); err != nil {
				return memhop.Capability{}, err
			}
			var st = memhop.CapabilityDraft
			switch a.Status {
			case "active":
				st = memhop.CapabilityActive
			case "deprecated":
				st = memhop.CapabilityDeprecated
			}
			patch.Status = &st
		}
		if len(a.Resources) > 0 || a.Workflow != nil {
			// Round-trip through JSON: resourceArg/workflowArg field names
			// match the core ResourceRef/Workflow DTOs exactly.
			payload, err := json.Marshal(map[string]any{
				"resources": a.Resources,
				"workflow":  a.Workflow,
			})
			if err != nil {
				return memhop.Capability{}, err
			}
			var nested memhop.CapabilityPatch
			if err := json.Unmarshal(payload, &nested); err != nil {
				return memhop.Capability{}, err
			}
			patch.Resources = nested.Resources
			patch.Workflow = nested.Workflow
		}
		cap, err := db.UpdateCapability(a.ID, patch)
		if err != nil {
			return memhop.Capability{}, err
		}
		return *cap, nil
	}))
}
