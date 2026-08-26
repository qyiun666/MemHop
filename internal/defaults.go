// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

// MemHopDefaults holds the host-facing business knobs of the memory engine.
// Engine tuning constants (RRF k, decay lambdas, L1 activation, scoring
// weights) live in tuning.go and are not configurable — they were internal
// tuning artifacts, not host settings.
type MemHopDefaults struct {
	Capacity                    int   `json:"capacity"`
	DreamCompressMinTopics      int   `json:"dream_compress_min_topics"`
	SearchDreamContextThreshold int   `json:"search_dream_context_threshold"`
	AgentIdleTTLMs              int64 `json:"agent_idle_ttl_ms"` // reclaim idle agent contexts after this many ms (0 disables)
}

// DefaultMemHopDefaults is the single hardcoded source of engine defaults.
var DefaultMemHopDefaults = &MemHopDefaults{
	Capacity:                    7,
	DreamCompressMinTopics:      20,
	SearchDreamContextThreshold: 30,      // Search triggers a scene Dream when its context exceeds this many topics
	AgentIdleTTLMs:              3600000, // 60 minutes of inactivity frees the agent's in-memory indices
}
