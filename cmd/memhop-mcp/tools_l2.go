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

type sceneRenameArgs struct {
	SceneID string `json:"scene_id"`
	Name    string `json:"name"`
}

type topicRenameArgs struct {
	TopicID string `json:"topic_id"`
	Name    string `json:"name"`
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
		Description: "读取 L0 宿主画像（名称、角色、个性、偏好，以及 Dream 蒸馏出的情绪状态与 MBTI 倾向）。",
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
		Description: "更新 L0 宿主画像的名称、角色、个性与偏好四项。Dream 蒸馏出的情绪状态与 MBTI 倾向由库内按现值保留，画像更新时间由库内戳写，本工具不碰这两项。",
		InputSchema: objSchema(map[string]any{
			"name":        strProp("宿主名称"),
			"role":        strProp("角色定位"),
			"personality": strProp("个性描述"),
			"preferences": mapProp("偏好键值对"),
		}),
	}, handle[profileUpdateArgs, updateResult](func(a profileUpdateArgs) (updateResult, error) {
		return updateResult{OK: true}, db.UpdateL0(&memhop.ProfileInput{
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
		return db.ListScenes("")
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
		Name:        "memhop_scene_rename",
		Description: "给一个 L2 场景（= 宿主会话）改名。新建时库按 \"session:<id>\" 命名，本工具是宿主换成人类可读标题的唯一入口；改名后后续读取/沉淀不会覆盖它。未知场景返回错误。",
		InputSchema: objSchema(map[string]any{
			"scene_id": strProp("场景 ID（16 位 hex），必填"),
			"name":     strProp("新场景名，必填非空"),
		}, "scene_id", "name"),
	}, handle[sceneRenameArgs, updateResult](func(a sceneRenameArgs) (updateResult, error) {
		_, err := db.UpdateScene(a.SceneID, memhop.ScenePatch{Name: &a.Name})
		return updateResult{OK: true}, err
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_topic_rename",
		Description: "给一轮（一个 L2 话题）起名。话题创建时无名，本工具是宿主给某一轮一个自己能认回来的指称的唯一入口；名字归宿主，引擎不派生，所以后续巩固与合并都不会覆盖它。空名被拒（空是「还没命名」而不是一个名字），未知话题返回错误。",
		InputSchema: objSchema(map[string]any{
			"topic_id": strProp("话题 ID（16 位 hex，即 memhop_search 返回的 new_topic_id），必填"),
			"name":     strProp("新话题名，必填非空"),
		}, "topic_id", "name"),
	}, handle[topicRenameArgs, updateResult](func(a topicRenameArgs) (updateResult, error) {
		_, err := db.RenameTopic(a.TopicID, a.Name)
		return updateResult{OK: true}, err
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
