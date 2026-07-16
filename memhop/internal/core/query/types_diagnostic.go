// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Diagnostic DTOs for the MemHop Query layer.

package query

// HealthStatus is the MemHop instance health report.
type HealthStatus struct {
	OK                bool           `json:"ok"`
	DBSizeBytes       uint64         `json:"db_size_bytes"`
	LayerCounts       map[string]int `json:"layer_counts"`
	LastDreamAt       *string        `json:"last_dream_at,omitempty"`
	EncoderConfigured bool           `json:"encoder_configured"`
	IVFIndexBuilt     bool           `json:"ivf_index_built"`
	Issues            []string       `json:"issues"`
}

// MemHopStats holds layer-level statistics.
type MemHopStats struct {
	L0ProfileExists bool    `json:"l0_profile_exists"`
	L1EngramCount   int     `json:"l1_engram_count"`
	L2TopicCount    int     `json:"l2_topic_count"`
	L3GraphCount    int     `json:"l3_graph_count"`
	L4ArchiveCount  int     `json:"l4_archive_count"`
	L5CrystalCount  int     `json:"l5_crystal_count"`
	DBSizeBytes     uint64  `json:"db_size_bytes"`
	IVFClusterCount int     `json:"ivf_cluster_count"`
	CacheHitRate    float64 `json:"cache_hit_rate"`
}
