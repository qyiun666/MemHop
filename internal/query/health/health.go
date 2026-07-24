// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Health check logic: layer counting, issue collection.

package health

import (
	"github.com/qyiun666/MemHop/internal/common/hash"
	"github.com/qyiun666/MemHop/internal/core/storage"
)

// CountLayers scans the engine and counts records by layer type.
func CountLayers(engine *storage.StorageEngine) map[string]int {
	counts := map[string]int{
		"l0_profile": 0, "l1_engram": 0, "l2_topic": 0,
		"l3_knowledge": 0, "l4_archive": 0, "l5_crystal": 0,
	}
	profileHash := hash.HashID("profile")
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, _, err := engine.ReadRecord(idHash)
		if err != nil {
			return true
		}
		switch rt {
		case storage.RecL0Profile:
			if idHash == profileHash {
				counts["l0_profile"]++
			}
		case storage.RecL1SceneNode:
			counts["l1_engram"]++
		case storage.RecL2Topic:
			counts["l2_topic"]++
		case storage.RecL3GraphSlot:
			counts["l3_knowledge"]++
		case storage.RecL4Archive:
			counts["l4_archive"]++
		case storage.RecL5ActionChain:
			counts["l5_crystal"]++
		}
		return true
	})
	return counts
}

// CollectIssues checks for common health issues.
func CollectIssues(enc interface{ IsAvailable() bool }, counts map[string]int) []string {
	var issues []string
	if enc == nil || !enc.IsAvailable() {
		issues = append(issues, "encoder not available")
	}
	if counts["l2_topic"] == 0 {
		issues = append(issues, "no L2 topics")
	}
	return issues
}
