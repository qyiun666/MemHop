// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L5 plugin tools.

package main

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop"
)

type pluginImportArgs struct {
	Path string `json:"path"`
}

type pluginIDArgs struct {
	ID string `json:"id"`
}

type pluginListArgs struct {
	Status     *string `json:"status,omitempty"`      // "draft" / "active" / "deprecated"
	PluginType *string `json:"plugin_type,omitempty"` // primary type label filter
	Keyword    string  `json:"keyword,omitempty"`     // name substring (case-insensitive)
}

func registerL5Tools(s *mcp.Server, db *memhop.DB) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_plugin_import",
		Description: "从 JSON 文件路径导入 L5 插件（PluginImport 结构：name/trigger/plugin_type/manifest）。ID 为 hash(name:trigger)，重复导入幂等。",
		InputSchema: objSchema(map[string]any{
			"path": strProp("插件描述 JSON 文件的本地路径，必填"),
		}, "path"),
	}, handle[pluginImportArgs, pluginIDResult](func(a pluginImportArgs) (pluginIDResult, error) {
		id, err := db.ImportPlugin(a.Path)
		if err != nil {
			return pluginIDResult{}, err
		}
		return pluginIDResult{PluginID: id}, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_plugin_get",
		Description: "按 ID 读取 L5 插件（含结构化 Manifest）。",
		InputSchema: objSchema(map[string]any{
			"id": strProp("插件 ID（16 位 hex），必填"),
		}, "id"),
	}, handle[pluginIDArgs, memhop.PluginSlot](func(a pluginIDArgs) (memhop.PluginSlot, error) {
		slot, err := db.GetPlugin(a.ID)
		if err != nil {
			return memhop.PluginSlot{}, err
		}
		return *slot, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_plugin_delete",
		Description: "删除 L5 插件记录。",
		InputSchema: objSchema(map[string]any{
			"id": strProp("插件 ID（16 位 hex），必填"),
		}, "id"),
	}, handle[pluginIDArgs, updateResult](func(a pluginIDArgs) (updateResult, error) {
		return updateResult{OK: true}, db.DeletePlugin(a.ID)
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_plugin_list",
		Description: "列出 L5 插件，支持按状态/类型/名称关键字过滤，按更新时间倒序。",
		InputSchema: objSchema(map[string]any{
			"status":      strProp("状态过滤：draft / active / deprecated"),
			"plugin_type": strProp("主类型标签过滤"),
			"keyword":     strProp("名称子串匹配（忽略大小写）"),
		}),
	}, handle[pluginListArgs, []memhop.PluginSlot](func(a pluginListArgs) ([]memhop.PluginSlot, error) {
		return db.ListPlugins(memhop.PluginListQuery{
			Status:     a.Status,
			PluginType: a.PluginType,
			Keyword:    a.Keyword,
		})
	}))
}

type pluginIDResult struct {
	PluginID string `json:"plugin_id"`
}
