// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 capability tools: import/get/delete/list/activate/usage/update.
//
// CapabilityPatch carries nested struct fields that the api package does not
// re-export by name; string params are mapped to the api enum constants
// (typed by inference) and the nested resources payload is round-tripped
// through JSON, whose field names match the core DTOs exactly.

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
	Package string `json:"package,omitempty"`
	Keyword string `json:"keyword,omitempty"`
}

// capabilityUpdateArgs carries the partial update fields of
// memhop_capability_update; empty strings mean "leave unchanged".
type capabilityUpdateArgs struct {
	ID        string        `json:"id"`
	Version   string        `json:"version,omitempty"`
	Summary   string        `json:"summary,omitempty"`
	Trigger   string        `json:"trigger,omitempty"`
	Status    string        `json:"status,omitempty"`
	Resources []resourceArg `json:"resources,omitempty"`
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

// resourceArrayProp is the JSON Schema for a []ResourceRef.
func resourceArrayProp(desc string) map[string]any {
	return map[string]any{
		"type": "array",
		"items": objSchema(map[string]any{
			"type":   strProp("mcp | skill | api | composite"),
			"name":   strProp("工具名（= ToolSpec.Name）"),
			"desc":   strProp("怎么调用（给 LLM，= ToolSpec.Desc）"),
			"input":  strProp("参数 JSON Schema 字符串（= ToolSpec.Input）"),
			"output": strProp("输出描述（= ToolSpec.Output）"),
			"ref":    strProp("mcp server 地址 / skill 路径 / api:Method / 命令"),
			"config": strProp(`连接配置（JSON，可选）；composite 条目放动作链 {"steps":[{"tool":"...","args":{...}}]}`),
		}),
		"description": desc,
	}
}

// registerL5Tools installs the capability tools; each register function
// owns one cohesive tool group. capDir anchors the path memhop_capability_import
// is given: the caller is an LLM, so the directory it may read from is the
// operator's choice, not the model's.
func registerL5Tools(s *mcp.Server, db *memhop.Session, capDir string) {
	registerCapabilityIOTools(s, db, capDir)
	registerCapabilityListTool(s, db)
	registerCapabilityLifecycleTools(s, db)
	registerCapabilityUpdateTool(s, db)
}

func registerCapabilityIOTools(s *mcp.Server, db *memhop.Session, capDir string) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_capability_import",
		Description: fmt.Sprintf("导入 memhop-capability/v4 能力包文件（文件或包含 capability.json 的目录）。一个包 = 名称 + 1..N 张能力卡；一张卡 = 名称 + N 个功能条目（resources），每个条目自带启动方式（type=mcp/skill/api/composite + ref/config）/说明（desc）/怎么用（input/output），与宿主 ToolSpec 同构；composite 条目在 config 放动作链。导入进文件级公共池，所有 agent 可见；同包重导入按卡名刷新定义、保留使用统计。path 相对服务端能力目录（--capability-dir，缺省为 --db-dir=%s）解析，越出该目录即拒绝。", capDir),
		InputSchema: objSchema(map[string]any{
			"path": strProp("能力包文件或目录路径（相对能力目录），必填"),
		}, "path"),
	}, handle[capabilityImportArgs, memhop.CapabilityImportResult](func(a capabilityImportArgs) (memhop.CapabilityImportResult, error) {
		path, err := resolveCapabilityPath(capDir, a.Path)
		if err != nil {
			return memhop.CapabilityImportResult{}, err
		}
		res, err := db.ImportCapability(path)
		if err != nil {
			return memhop.CapabilityImportResult{}, err
		}
		return *res, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_capability_get",
		Description: "按 ID 读取一个 L5 能力（含内置能力卡）。",
		InputSchema: objSchema(map[string]any{
			"id": strProp("能力 ID（16 位 hex），必填"),
		}, "id"),
	}, handle[capabilityIDArgs, memhop.Capability](func(a capabilityIDArgs) (memhop.Capability, error) {
		caps, err := db.ListCapabilities(memhop.CapabilityListQuery{IDs: []string{a.ID}})
		if err != nil {
			return memhop.Capability{}, err
		}
		if len(caps) == 0 {
			return memhop.Capability{}, fmt.Errorf("capability %s not found", a.ID)
		}
		return caps[0], nil
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
		Description: "列出 L5 能力（含内置能力卡）。可按状态（draft/active/deprecated）、来源包（package）与关键词过滤。",
		InputSchema: objSchema(map[string]any{
			"status":  strProp("状态过滤：draft | active | deprecated"),
			"package": strProp("来源包名过滤（导入文档的 name）"),
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
		if a.Package != "" {
			q.Package = &a.Package
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
			"summary":   strProp("能力摘要"),
			"trigger":   strProp("触发条件描述"),
			"status":    strProp("状态：draft | active | deprecated"),
			"resources": resourceArrayProp("功能条目列表"),
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
	if a.Status != "" {
		st, err := parseCapabilityStatus(a.Status)
		if err != nil {
			return patch, err
		}
		patch.Status = st
	}
	if len(a.Resources) > 0 {
		payload, err := json.Marshal(map[string]any{"resources": a.Resources})
		if err != nil {
			return patch, err
		}
		var nested memhop.CapabilityPatch
		if err := json.Unmarshal(payload, &nested); err != nil {
			return patch, err
		}
		patch.Resources = nested.Resources
	}
	return patch, nil
}
