// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package config

// MemHopDefaults holds the tuning knobs a caller supplies: the consolidation
// thresholds and the idle-domain TTL. There is no read-side calibration knob —
// a read never guesses which scene a message belongs to.
//
// One vocabulary across the four fields: 0 means "not filled", and the library default
// answers; a negative is the explicit spelling for "switch this knob off".
type MemHopDefaults struct {
	// SceneDreamTopicThreshold is how many depth-1 topics one scene may
	// accumulate before its Dream is scheduled. Negative disables the trigger.
	SceneDreamTopicThreshold int `json:"scene_dream_topic_threshold"`
	// DreamCompressMinTopics is the smallest depth-1 topic count a Dream pass
	// compresses; below it a scene keeps raw detail. It is also the target the model
	// is told to compress toward, so negative asks for no floor at all — the model
	// merges as far as its own rules allow.
	DreamCompressMinTopics int `json:"dream_compress_min_topics"`
	// AgentIdleTTLMs reclaims an idle agent's in-memory contexts. Negative disables
	// the reclaim.
	AgentIdleTTLMs int64 `json:"agent_idle_ttl_ms"`
	// ContentRetentionMs is how long a turn's records (L4 content and L5 plan
	// nodes) outlive it before a Dream sweeps them. There is no spelling for "keep
	// everything": retention is what bounds the file, so a host needing longer-lived
	// originals raises the window rather than turning it off.
	ContentRetentionMs int64 `json:"content_retention_ms"`
}

// DefaultMemHopDefaults is the single hardcoded source of engine defaults. The trigger
// sits just above the compress floor so a scheduled Dream always has something to
// consolidate. It is a value, not a pointer: a caller that wants different knobs
// copies it and edits the copy, so no caller can change what every other caller reads.
var DefaultMemHopDefaults = MemHopDefaults{
	SceneDreamTopicThreshold: 24,
	DreamCompressMinTopics:   20,
	AgentIdleTTLMs:           3600000,                 // 60 minutes
	ContentRetentionMs:       7 * 24 * 60 * 60 * 1000, // seven days
}

// Normalized reads a caller's struct the way the vocabulary above says to read it: an
// unfilled knob (0) takes the library default, a negative takes the off spelling. It
// runs once, where the host's struct becomes the engine's, so no reader downstream has
// to guess whether a zero meant absence — and a zero never means the hazardous thing it
// used to: `DreamCompressMinTopics` doubles as the number the consolidation prompt tells
// the model to compress a scene down toward, so a zero there asked for maximum merging.
func (m MemHopDefaults) Normalized() MemHopDefaults {
	out := m
	if out.SceneDreamTopicThreshold == 0 {
		out.SceneDreamTopicThreshold = DefaultMemHopDefaults.SceneDreamTopicThreshold
	}
	switch {
	case out.DreamCompressMinTopics == 0:
		out.DreamCompressMinTopics = DefaultMemHopDefaults.DreamCompressMinTopics
	case out.DreamCompressMinTopics < 0:
		out.DreamCompressMinTopics = 0
	}
	if out.AgentIdleTTLMs == 0 {
		out.AgentIdleTTLMs = DefaultMemHopDefaults.AgentIdleTTLMs
	}
	if out.ContentRetentionMs <= 0 {
		out.ContentRetentionMs = DefaultMemHopDefaults.ContentRetentionMs
	}
	return out
}
