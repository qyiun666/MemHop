// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 capability tools.

package main

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop"
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
		Description: "导入 memhop-capability/v2 能力文件（文件或包含 capability.json 的目录）。能力是对宿主资源的封装：type=mcp（单个 mcp 工具）、type=skill（单个 skill）、type=composite（多个 mcp/skill 集合，可选 workflow 编排）。",
		InputSchema: objSchema(map[string]any{
			"path": strProp("能力 JSON 文件路径，必填"),
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
		Description: "按 ID 读取一个 L5 能力。",
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
		Description: "删除一个 L5 能力记录。",
		InputSchema: objSchema(map[string]any{
			"id": strProp("能力 ID（16 位 hex），必填"),
		}, "id"),
	}, handle[capabilityIDArgs, updateResult](func(a capabilityIDArgs) (updateResult, error) {
		return updateResult{OK: true}, db.DeleteCapability(a.ID)
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_capability_list",
		Description: "列出 L5 能力，支持按 status / type / keyword 过滤。",
		InputSchema: objSchema(map[string]any{
			"status":  strProp("状态过滤：draft / active / deprecated"),
			"type":    strProp("类型过滤：mcp / skill / composite"),
			"keyword": strProp("名称/摘要/触发条件关键字过滤"),
		}),
	}, handle[capabilityListArgs, []memhop.Capability](func(a capabilityListArgs) ([]memhop.Capability, error) {
		q := memhop.CapabilityListQuery{Keyword: a.Keyword}
		if a.Status != "" {
			var st memhop.CapabilityStatus
			switch a.Status {
			case "draft":
				st = memhop.CapabilityDraft
			case "active":
				st = memhop.CapabilityActive
			case "deprecated":
				st = memhop.CapabilityDeprecated
			default:
				return nil, invalidCapabilityStatusError(a.Status)
			}
			q.Status = &st
		}
		if a.Type != "" {
			typ := memhop.CapabilityType(a.Type)
			if typ != memhop.CapabilityMCP && typ != memhop.CapabilitySkill && typ != memhop.CapabilityComposite {
				return nil, invalidCapabilityTypeError(a.Type)
			}
			q.Type = &typ
		}
		return db.ListCapabilities(q)
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_capability_activate",
		Description: "激活一个 draft 能力（L7 结晶生成的能力需要宿主确认后激活）。",
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
		Description: "记录一次能力使用结果（success=true/false），更新 TriggerCount / SuccessRate / LastTriggered。",
		InputSchema: objSchema(map[string]any{
			"id":      strProp("能力 ID（16 位 hex），必填"),
			"success": boolProp("本次使用是否成功，必填"),
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
		Description: "部分更新一个 L5 能力（name 不可改，改名需先删后导入）；内置能力只读，拒绝更新。空字符串/空数组字段表示不修改。",
		InputSchema: objSchema(map[string]any{
			"id":        strProp("能力 ID（16 位 hex），必填"),
			"version":   strProp("版本号"),
			"type":      strProp("类型：mcp / skill / composite"),
			"summary":   strProp("插件介绍（给 LLM 的一句话）"),
			"trigger":   strProp("触发关键词"),
			"status":    strProp("状态：draft / active / deprecated"),
			"resources": resourceArrayProp("封装的资源列表（mcp/skill）"),
			"workflow":  workflowProp(),
		}, "id"),
	}, handle[capabilityUpdateArgs, memhop.Capability](func(a capabilityUpdateArgs) (memhop.Capability, error) {
		patch := memhop.CapabilityPatch{}
		if a.Version != "" {
			patch.Version = &a.Version
		}
		if a.Type != "" {
			typ := memhop.CapabilityType(a.Type)
			if typ != memhop.CapabilityMCP && typ != memhop.CapabilitySkill && typ != memhop.CapabilityComposite {
				return memhop.Capability{}, invalidCapabilityTypeError(a.Type)
			}
			patch.Type = &typ
		}
		if a.Summary != "" {
			patch.Summary = &a.Summary
		}
		if a.Trigger != "" {
			patch.Trigger = &a.Trigger
		}
		if a.Status != "" {
			var st memhop.CapabilityStatus
			switch a.Status {
			case "draft":
				st = memhop.CapabilityDraft
			case "active":
				st = memhop.CapabilityActive
			case "deprecated":
				st = memhop.CapabilityDeprecated
			default:
				return memhop.Capability{}, invalidCapabilityStatusError(a.Status)
			}
			patch.Status = &st
		}
		if a.Resources != nil {
			res := make([]memhop.ResourceRef, 0, len(a.Resources))
			for _, r := range a.Resources {
				res = append(res, memhop.ResourceRef{
					Type:        memhop.CapabilityType(r.Type),
					Name:        r.Name,
					Ref:         r.Ref,
					Description: r.Description,
					Config:      r.Config,
				})
			}
			patch.Resources = &res
		}
		if a.Workflow != nil {
			steps := make([]memhop.WorkflowStep, 0, len(a.Workflow.Steps))
			for _, st := range a.Workflow.Steps {
				steps = append(steps, memhop.WorkflowStep{Ref: st.Ref, Action: st.Action})
			}
			patch.Workflow = &memhop.Workflow{Steps: steps}
		}
		cap, err := db.UpdateCapability(a.ID, patch)
		if err != nil {
			return memhop.Capability{}, err
		}
		return *cap, nil
	}))
}

func invalidCapabilityStatusError(s string) error {
	return fmt.Errorf("invalid capability status %q: must be draft, active or deprecated", s)
}

func invalidCapabilityTypeError(s string) error {
	return fmt.Errorf("invalid capability type %q: must be mcp, skill or composite", s)
}
