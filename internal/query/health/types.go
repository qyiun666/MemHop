// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Health/Diagnostic DTOs for the MemHop Query layer.

package health

// HealthStatus is the MemHop instance health report.
type HealthStatus struct {
	OK                bool           `json:"ok"`
	DBSizeBytes       uint64         `json:"db_size_bytes"`
	LayerCounts       map[string]int `json:"layer_counts"`
	LastDreamAt       *string        `json:"last_dream_at,omitempty"`
	EncoderConfigured bool           `json:"encoder_configured"`
	Issues            []string       `json:"issues"`
}
