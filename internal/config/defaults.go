// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package config

import (
	"fmt"
	"math"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
)

// MemHopDefaults holds the tuning knobs a caller supplies: the consolidation
// thresholds and the idle-domain TTL. There is no read-side calibration knob —
// a read never guesses which scene a message belongs to.
//
// One vocabulary across three of the four fields: 0 means "not filled", and the library
// default answers; a negative is the explicit spelling for "switch this knob off".
// ContentRetentionMs is the exception and reads its own comment: it has no off spelling,
// so Validate refuses the values it cannot honour before the file is ever touched.
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
	// originals raises the window rather than turning it off. "Off" and "so long it
	// never sweeps" are therefore both refused by Validate — the second because the
	// sweep measures in milliseconds and a window past MaxContentRetentionMs wraps
	// the duration around, landing the cutoff in the future and reading every record
	// as expired.
	ContentRetentionMs int64 `json:"content_retention_ms"`
}

// MaxContentRetentionMs is the largest window the sweep can represent: one whose
// millisecond count still fits a time.Duration when scaled to nanoseconds. Past it the
// multiplication wraps, and a wrapped window is the aggressive one, not the conservative
// one — see ContentRetentionMs.
const MaxContentRetentionMs = int64(math.MaxInt64 / int64(time.Millisecond))

// Validate refuses a retention window the engine cannot honour. Every other knob has an
// off spelling, so this checks the one that does not: a negative asks for a sweep that
// never runs, and a window past the representable ceiling asks for one that sweeps
// everything. Both answer an error instead of a silently redrawn window, and OpenDB runs
// this before it touches the filesystem.
func (m MemHopDefaults) Validate() error {
	switch {
	case m.ContentRetentionMs < 0:
		return common.NewError(common.ErrConfig, fmt.Sprintf(
			"content_retention_ms %d asks to switch the sweep off, which the engine does not offer: retention is what bounds the file, so leave it 0 for the default (%d ms) or name a window you mean to keep",
			m.ContentRetentionMs, DefaultMemHopDefaults.ContentRetentionMs))
	case m.ContentRetentionMs > MaxContentRetentionMs:
		return common.NewError(common.ErrConfig, fmt.Sprintf(
			"content_retention_ms %d is past the longest window the sweep can measure (%d ms): a longer one wraps the duration around and puts the cutoff in the future, which reads every record in the domain as expired",
			m.ContentRetentionMs, MaxContentRetentionMs))
	}
	return nil
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
// A host's retention window reaches this already validated (Validate refuses the values
// that have no meaning); an internal caller building a struct by hand gets the engine
// default for a non-positive one, which is what dream's own unconfigured answer is.
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
