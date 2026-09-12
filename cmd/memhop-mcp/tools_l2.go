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
		Description: "读取 L0 宿主画像（名称、角色、个性、偏好，以及 Dream 蒸馏出的情绪状态与 MBTI 倾向）。两组蒸馏信号都是带标度的数：emotion_state 的 valence/arousal/dominance 各在 0..1——valence 0=极负面、0.5=中性、1=极正面，arousal 0=平静、1=高度激动，dominance 0=顺从、1=支配——两端都是合法读数，0 不是「没测过」；mbti 的 i_e/n_s/t_f/j_p 各在 -1..1，负=I/N/T/J、正=E/S/F/P、幅度即强度，type 由这四轴推出（某轴为 0 即在类型词里记 X，四轴全静默就不给类型词）。从未蒸馏过的域两组都是全零、type 为空。",
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
		Description: "更新 L0 宿主画像。四项是整写而不是打补丁：这四项写进去就是画像的宿主半区，没填的那几项会被清空（要保留现值就把它一起提交），name 必填——域就靠它被称呼，空白名会被拒绝。Dream 蒸馏出的情绪状态与 MBTI 倾向由库内按现值保留，画像更新时间由库内戳写，域身份在建域时已定，这三项本工具的入参里根本没有，也就传不进来。",
		InputSchema: objSchema(map[string]any{
			"name":        strProp("宿主名称，必填非空"),
			"role":        strProp("角色定位"),
			"personality": strProp("个性描述"),
			"preferences": mapProp("偏好键值对"),
		}, "name"),
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
		Description: "列出所有 L2 场景（= 宿主会话）：场景 ID、名称，以及它锚定的 L3 项目域（未锚定则结果里没有这个键）。场景不带话题条数——要数一个场景里的话题请用 memhop_scene_topics。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[[]memhop.SceneSlot](func() ([]memhop.SceneSlot, error) {
		return db.ListScenes("")
	}))
}

func registerSceneDetailTools(s *mcp.Server, db *memhop.Session) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_scene_topics",
		Description: "读取 L2 场景上下文：场景内 depth ≤ 2 的话题元信息（不含 L4 消息）。返回里没有 parent_id——父子由 depth 与 child_count 判定：depth 1 且 child_count>0 的那条是 Dream 融合出的父话题，depth 2 的条目就是被它吞掉的轮次。话题下的 L4 对话原文请用 memhop_archive_search（topic_id 参数）单独查询。注意代价：本工具在域锁内读回整个场景的对话原文再丢弃，是工具面最重的一次读，只要元信息也付这一份。未知场景返回错误。",
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
