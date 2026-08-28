// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 tools: L0 profile (host identity) plus L2 scene/topic operations.

package main

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop/api"
)

type profileUpdateArgs struct {
	Name        string            `json:"name,omitempty"`
	Role        string            `json:"role,omitempty"`
	Personality string            `json:"personality,omitempty"`
	Preferences map[string]string `json:"preferences,omitempty"`
}

type sceneTopicsArgs struct {
	SceneID string `json:"scene_id"`
}

type sceneMergeArgs struct {
	PrimaryID    string   `json:"primary_id"`
	SecondaryIDs []string `json:"secondary_ids"`
}

// registerL2Tools installs the L0 profile and L2 scene tools; each
// register function owns one cohesive tool group.
func registerL2Tools(s *mcp.Server, db *memhop.Session) {
	registerProfileTools(s, db)
	registerSceneListTools(s, db)
	registerSceneDetailTools(s, db)
}

func registerProfileTools(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_profile_get",
		Description: "读取 L0 宿主画像（角色、个性、偏好、词表、风格与情绪模式）。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[memhop.ProfileSlot](func() (memhop.ProfileSlot, error) {
		slot, err := db.GetL0()
		if err != nil {
			return memhop.ProfileSlot{}, err
		}
		return *slot, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_profile_update",
		Description: "整体更新 L0 宿主画像（全量覆盖，缺省字段会被清空）。",
		InputSchema: objSchema(map[string]any{
			"name":        strProp("宿主名称"),
			"role":        strProp("角色定位"),
			"personality": strProp("个性描述"),
			"preferences": mapProp("偏好键值对"),
		}),
	}, handle[profileUpdateArgs, updateResult](func(a profileUpdateArgs) (updateResult, error) {
		return updateResult{OK: true}, db.UpdateL0(&memhop.ProfileSlot{
			Name:        a.Name,
			Role:        a.Role,
			Personality: a.Personality,
			Preferences: a.Preferences,
		})
	}))
}

func registerSceneListTools(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_scene_list",
		Description: "列出所有 L2 场景（场景 ID、名称与 depth1 话题条数）。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[[]memhop.SceneSlot](func() ([]memhop.SceneSlot, error) {
		return db.ListScenes()
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_scene_active_list",
		Description: "列出内存中激活的 L2 场景（dream 巩固目标）及其 depth1 话题条数；未激活的场景可搜索但不可巩固。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[[]memhop.SceneSlot](func() ([]memhop.SceneSlot, error) {
		ids := db.ActiveSceneIDs()
		if len(ids) == 0 {
			return []memhop.SceneSlot{}, nil
		}
		active := make(map[string]bool, len(ids))
		for _, id := range ids {
			active[id] = true
		}
		all, err := db.ListScenes()
		if err != nil {
			return nil, err
		}
		out := make([]memhop.SceneSlot, 0, len(ids))
		for _, sc := range all {
			if active[sc.SceneID] {
				out = append(out, sc)
			}
		}
		return out, nil
	}))
}

func registerSceneDetailTools(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_scene_topics",
		Description: "读取 L2 场景上下文：场景内 depth-1 话题元信息（不含 L4 消息）；话题下的 L4 对话原文请用 memhop_archive_search（topic_id 参数）单独查询。未知场景返回错误。",
		InputSchema: objSchema(map[string]any{
			"scene_id": strProp("场景 ID（16 位 hex），必填"),
		}, "scene_id"),
	}, handle[sceneTopicsArgs, memhop.SceneContext](func(a sceneTopicsArgs) (memhop.SceneContext, error) {
		ctx, err := db.SceneContext(a.SceneID)
		if err != nil {
			return memhop.SceneContext{}, err
		}
		// L4 messages are fetched on demand via memhop_archive_search;
		// strip them here so this tool returns pure L2 topic metadata.
		for i := range ctx.Topics {
			ctx.Topics[i].Messages = nil
		}
		return *ctx, nil
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_scene_merge",
		Description: "将次要场景的所有话题合并到主场景，并删除次要场景记录。",
		InputSchema: objSchema(map[string]any{
			"primary_id":    strProp("主场景 ID（16 位 hex），必填"),
			"secondary_ids": arrProp("次要场景 ID 列表（16 位 hex），必填", "string"),
		}, "primary_id", "secondary_ids"),
	}, handle[sceneMergeArgs, updateResult](func(a sceneMergeArgs) (updateResult, error) {
		return updateResult{OK: true}, db.MergeScenes(a.PrimaryID, a.SecondaryIDs)
	}))
}
