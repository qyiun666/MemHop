// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package config

// MemHopDefaults holds the tuning knobs a caller supplies: the consolidation
// thresholds and the idle-domain TTL. There is no read-side calibration knob —
// a read never guesses which scene a message belongs to.
type MemHopDefaults struct {
	// SceneDreamTopicThreshold is how many depth-1 topics one scene may
	// accumulate before its Dream is scheduled (<=0 disables the trigger).
	SceneDreamTopicThreshold int `json:"scene_dream_topic_threshold"`
	// DreamCompressMinTopics is the smallest depth-1 topic count a Dream pass
	// compresses; below it a scene keeps raw detail.
	DreamCompressMinTopics int `json:"dream_compress_min_topics"`
	// AgentIdleTTLMs reclaims an idle agent's in-memory contexts (0 disables).
	AgentIdleTTLMs int64 `json:"agent_idle_ttl_ms"`
}

// DefaultMemHopDefaults is the single hardcoded source of engine defaults.
// The trigger sits just above the compress floor so a scheduled Dream always
// has something to consolidate. It is a value, not a pointer: a caller that
// wants different knobs copies it and edits the copy, so no caller can change
// the defaults every other caller reads.
var DefaultMemHopDefaults = MemHopDefaults{
	SceneDreamTopicThreshold: 24,
	DreamCompressMinTopics:   20,
	AgentIdleTTLMs:           3600000, // 60 minutes of inactivity frees the agent's in-memory indices
}
