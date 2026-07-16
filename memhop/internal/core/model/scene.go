// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 SceneSlot — per-scene metadata (context.rs).

package model

import "github.com/qyiun666/memhop/memhop/internal/hash"

// SceneSlot holds lightweight per-scene metadata: scene_id + scene_name.
type SceneSlot struct {
	SceneID   uint64 `json:"scene_id"`
	SceneName string `json:"scene_name"`
}

// NewSceneSlot creates a SceneSlot with scene_id computed from the name.
func NewSceneSlot(name string) SceneSlot {
	return SceneSlot{
		SceneID:   hash.HashID(name),
		SceneName: name,
	}
}
