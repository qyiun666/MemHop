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
	Type   string  `json:"type"`
	Name   string  `json:"name"`
	Desc   string  `json:"desc"`
	Input  string  `json:"input,omitempty"`
	Output string  `json:"output,omitempty"`
	Ref    string  `json:"ref,omitempty"`
	Config *string `json:"config,omitempty"`
}

type workflowArg struct {
	Steps []workflowStepArg `json:"steps"`
}

type workflowStepArg struct {
	Ref    string         `json:"ref"`
	Action string         `json:"action,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
}

// validCapabilityStatus validates a status string before the typed switch.
func validCapabilityStatus(s string) error {
	switch s {
	case "draft", "active", "deprecated":
		return nil
	}
	return fmt.Errorf("invalid capability status %q (want draft, active or deprecated)", s)
}

// parseCapabilityStatus maps a validated status string to the api enum
// (shared by list filtering and partial update).
func parseCapabilityStatus(s string) (*memhop.CapabilityStatus, error) {
	if err := validCapabilityStatus(s); err != nil {
		return nil, err
	}
	st := memhop.CapabilityDraft
	switch s {
	case "active":
		st = memhop.CapabilityActive
	case "deprecated":
		st = memhop.CapabilityDeprecated
	}
	return &st, nil
}

// parseCapabilityType maps a validated type string to the api enum (shared
// by list filtering and partial update).
func parseCapabilityType(s string) (*memhop.CapabilityType, error) {
	if err := validCapabilityType(s); err != nil {
		return nil, err
	}
	typ := memhop.CapabilityMCP
	switch s {
	case "skill":
		typ = memhop.CapabilitySkill
	case "api":
		typ = memhop.CapabilityAPI
	case "composite":
		typ = memhop.CapabilityComposite
	}
	return &typ, nil
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
			"type":   strProp("mcp | skill | api"),
			"name":   strProp("工具名（= ToolSpec.Name）"),
			"desc":   strProp("怎么调用（给 LLM，= ToolSpec.Desc）"),
			"input":  strProp("参数 JSON Schema 字符串（= ToolSpec.Input）"),
			"output": strProp("输出描述（= ToolSpec.Output）"),
			"ref":    strProp("mcp server 地址 / skill 路径 / api:Method / 命令"),
			"config": strProp("连接配置（JSON，可选）"),
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
					"args":   strProp("步骤参数（JSON 对象，可选）"),
				}),
				"description": "有序编排步骤",
			},
		},
		"description": "composite 能力的编排（可选）",
	}
}

// registerL5Tools installs the capability tools; each register function
// owns one cohesive tool group.
func registerL5Tools(s *mcp.Server, db *memhop.Session) {
	registerCapabilityIOTools(s, db)
	registerCapabilityListTool(s, db)
	registerCapabilityLifecycleTools(s, db)
	registerCapabilityUpdateTool(s, db)
}

func registerCapabilityIOTools(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_capability_import",
		Description: "导入 memhop-capability/v3 能力文件（文件或包含 capability.json 的目录）。能力是对宿主资源的封装：type=mcp（单个 mcp 工具）、type=skill（单个 skill）、type=api（单个 api 方法）、type=composite（多个 mcp/skill/api 集合，可选 workflow 编排）；资源即工具声明（name/desc/input/output 与宿主 ToolSpec 同构）。",
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
}

func registerCapabilityListTool(s *mcp.Server, db *memhop.Session) {
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
			st, err := parseCapabilityStatus(a.Status)
			if err != nil {
				return nil, err
			}
			q.Status = st
		}
		if a.Type != "" {
			typ, err := parseCapabilityType(a.Type)
			if err != nil {
				return nil, err
			}
			q.Type = typ
		}
		q.Keyword = a.Keyword
		return db.ListCapabilities(q)
	}))
}

// registerCapabilityLifecycleTools installs activate/usage over one capability.
func registerCapabilityLifecycleTools(s *mcp.Server, db *memhop.Session) {
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
}

func registerCapabilityUpdateTool(s *mcp.Server, db *memhop.Session) {
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
		patch, err := buildCapabilityPatch(a)
		if err != nil {
			return memhop.Capability{}, err
		}
		cap, err := db.UpdateCapability(a.ID, patch)
		if err != nil {
			return memhop.Capability{}, err
		}
		return *cap, nil
	}))
}

// buildCapabilityPatch converts the update request into a partial patch
// (empty strings mean "leave unchanged").
func buildCapabilityPatch(a capabilityUpdateArgs) (memhop.CapabilityPatch, error) {
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
		typ, err := parseCapabilityType(a.Type)
		if err != nil {
			return patch, err
		}
		patch.Type = typ
	}
	if a.Status != "" {
		st, err := parseCapabilityStatus(a.Status)
		if err != nil {
			return patch, err
		}
		patch.Status = st
	}
	if len(a.Resources) > 0 || a.Workflow != nil {
		if err := applyNestedPatch(&patch, a); err != nil {
			return patch, err
		}
	}
	return patch, nil
}

// applyNestedPatch round-trips the nested resources/workflow payloads
// through JSON: resourceArg/workflowArg field names match the core
// ResourceRef/Workflow DTOs exactly.
func applyNestedPatch(patch *memhop.CapabilityPatch, a capabilityUpdateArgs) error {
	payload, err := json.Marshal(map[string]any{
		"resources": a.Resources,
		"workflow":  a.Workflow,
	})
	if err != nil {
		return err
	}
	var nested memhop.CapabilityPatch
	if err := json.Unmarshal(payload, &nested); err != nil {
		return err
	}
	patch.Resources = nested.Resources
	patch.Workflow = nested.Workflow
	return nil
}
