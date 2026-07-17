// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package model

import "memhop/internal/hash"

// L2场景
type SceneSlot struct {
	SceneID   uint64 `json:"scene_id"`
	SceneName string `json:"scene_name"`
}

func NewSceneSlot(name string) SceneSlot {
	return SceneSlot{
		SceneID:   hash.HashID(name),
		SceneName: name,
	}
}
