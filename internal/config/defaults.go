// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package config

// MemHopDefaults holds the host-facing business knobs of the memory engine:
// consolidation thresholds and the idle-domain TTL. Retrieval scoring tuning
// retired with the scene-scoring subsystem — Search no longer guesses which
// scene a message belongs to, so there is nothing to calibrate on the read
// side.
type MemHopDefaults struct {
	// SceneDreamTopicThreshold is how many depth-1 topics one scene may
	// accumulate before Update schedules that scene's Dream (<=0 disables
	// the trigger). A scene is a host session, so this bounds the context a
	// host reads back.
	SceneDreamTopicThreshold int `json:"scene_dream_topic_threshold"`
	// DreamCompressMinTopics is the smallest depth-1 topic count a Dream pass
	// compresses; below it a scene keeps raw detail.
	DreamCompressMinTopics int `json:"dream_compress_min_topics"`
	// AgentIdleTTLMs reclaims an idle agent's in-memory contexts (0 disables).
	AgentIdleTTLMs int64 `json:"agent_idle_ttl_ms"`
}

// DefaultMemHopDefaults is the single hardcoded source of engine defaults.
// The trigger sits just above the compress floor so a scheduled Dream always
// has something to consolidate.
var DefaultMemHopDefaults = &MemHopDefaults{
	SceneDreamTopicThreshold: 24,
	DreamCompressMinTopics:   20,
	AgentIdleTTLMs:           3600000, // 60 minutes of inactivity frees the agent's in-memory indices
}
