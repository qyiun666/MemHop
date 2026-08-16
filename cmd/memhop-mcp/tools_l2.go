// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L0 profile and L2 scene tools.

package main

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	memhop "github.com/qyiun666/MemHop"
)

type profileArgs struct {
	Name            string            `json:"name,omitempty"`
	Role            string            `json:"role,omitempty"`
	Personality     string            `json:"personality,omitempty"`
	Preferences     map[string]string `json:"preferences,omitempty"`
	Lexicon         map[string]string `json:"lexicon,omitempty"`
	StyleTraits     []string          `json:"style_traits,omitempty"`
	EmotionPatterns map[string]string `json:"emotion_patterns,omitempty"`
}

type sceneMergeArgs struct {
	PrimaryID    string   `json:"primary_id"`
	SecondaryIDs []string `json:"secondary_ids"`
}

func registerL2Tools(s *mcp.Server, db *memhop.DB) {
	s.AddTool(&mcp.Tool{
		Name:        "memhop_profile_get",
		Description: "读取 L0 档案（Agent 画像单例）：姓名、角色、性格、世界观、偏好、词汇、风格特征与情绪模式。",
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
		Description: "覆写 L0 档案（整体覆盖，未提供的字段将被清空）。字段：name/role/personality/preferences/lexicon/style_traits/emotion_patterns。",
		InputSchema: objSchema(map[string]any{
			"name":             strProp("档案名称"),
			"role":             strProp("角色"),
			"personality":      strProp("性格"),
			"preferences":      strProp("偏好键值对"),
			"lexicon":          strProp("词汇表键值对"),
			"style_traits":     arrProp("风格特征", "string"),
			"emotion_patterns": strProp("情绪模式键值对"),
		}),
	}, handle[profileArgs, updateResult](func(a profileArgs) (updateResult, error) {
		slot := &memhop.ProfileSlot{
			Name:            a.Name,
			Role:            a.Role,
			Personality:     a.Personality,
			Preferences:     a.Preferences,
			Lexicon:         a.Lexicon,
			StyleTraits:     a.StyleTraits,
			EmotionPatterns: a.EmotionPatterns,
		}
		return updateResult{OK: true}, db.UpdateL0(slot)
	}))

	s.AddTool(&mcp.Tool{
		Name:        "memhop_scene_list",
		Description: "列出所有 L2 场景（场景 ID 与名称）。",
		InputSchema: objSchema(nil),
	}, handleNoArgs[[]memhop.SceneSlot](func() ([]memhop.SceneSlot, error) {
		return db.ListScenes()
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
