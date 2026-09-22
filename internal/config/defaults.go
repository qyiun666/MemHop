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
	// compresses; below it a scene keeps raw detail. Unlike the two knobs beside it,
	// 0 does not disable anything: a floor of 0 leaves no scene out of the pass, and
	// the same number is the target the model is told to compress toward, so 0 asks
	// it to merge as far as its own rules allow.
	DreamCompressMinTopics int `json:"dream_compress_min_topics"`
	// AgentIdleTTLMs reclaims an idle agent's in-memory contexts (0 disables).
	AgentIdleTTLMs int64 `json:"agent_idle_ttl_ms"`
	// ContentRetentionMs is how long a turn's records (L4 content and L5 plan
	// nodes) outlive it before a Dream sweeps them. A value of 0 or less means
	// the library default — seven days, the window the engine ships with. There
	// is no "keep everything" spelling: retention is what bounds the file, and
	// a host that needs longer-lived originals raises the window rather than
	// turning the sweep off.
	ContentRetentionMs int64 `json:"content_retention_ms"`
}

// DefaultMemHopDefaults is the single hardcoded source of engine defaults.
// The trigger sits just above the compress floor so a scheduled Dream always
// has something to consolidate. It is a value, not a pointer: a caller that
// wants different knobs copies it and edits the copy, so no caller can change
// the defaults every other caller reads.
var DefaultMemHopDefaults = MemHopDefaults{
	SceneDreamTopicThreshold: 24,
	DreamCompressMinTopics:   20,
	AgentIdleTTLMs:           3600000,                 // 60 minutes of inactivity frees the agent's in-memory indices
	ContentRetentionMs:       7 * 24 * 60 * 60 * 1000, // seven days, the engine's own window
}
