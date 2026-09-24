// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Which conversation a domain resumes after a restart is not a detail: it decides what the
// next `Search` reads back, writes into, and distils. The counter alone could not answer
// it — two conversations of the same length tie, and the old tie-break picked the smaller
// id, so a restart sometimes dropped the host into a stream it had not been using. These
// cases pin the rule on records written by hand, so nothing depends on clock resolution:
// recency first, the counter only where nothing was ever stamped, the smaller id last.

package internal

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/scene"
)

func writeScene(t *testing.T, engine *core.StorageEngine, id, turnSeq uint64, usedAt int64) {
	t.Helper()
	slot := core.SceneSlot{SceneID: id, SceneName: "session:x", TurnSeq: turnSeq, LastUsedAt: usedAt}
	if err := core.WriteSceneSlot(engine, agentForScene, id, &slot); err != nil {
		t.Fatalf("write scene %x: %v", id, err)
	}
}

const agentForScene = uint64(3)

func TestCurrentSceneResumesTheConversationInUse(t *testing.T) {
	engine := newTestEngine(t)

	// Equal counters, and the more recently used one carries the larger id: recency wins,
	// where the old rule would have restored the smaller id instead.
	writeScene(t, engine, 0x20, 3, 2_000)
	writeScene(t, engine, 0x10, 3, 1_000)
	if got := mustResume(t, engine); got != 0x20 {
		t.Fatalf("resumed scene %x, want the one a turn was opened in last", got)
	}

	// A long conversation nobody touches versus a short one just used: still the used one.
	fresh := newTestEngine(t)
	writeScene(t, fresh, 0x30, 9, 1_000)
	writeScene(t, fresh, 0x40, 1, 5_000)
	if got := mustResume(t, fresh); got != 0x40 {
		t.Fatalf("resumed scene %x, want recency to outrank the counter", got)
	}

	// Nothing stamped — a file written before the field existed: the historical rule,
	// furthest counter, and the smaller id breaking a counter tie.
	unstamped := newTestEngine(t)
	writeScene(t, unstamped, 0x60, 2, 0)
	writeScene(t, unstamped, 0x50, 5, 0)
	if got := mustResume(t, unstamped); got != 0x50 {
		t.Fatalf("resumed scene %x, want the furthest counter when nothing is stamped", got)
	}
	tied := newTestEngine(t)
	writeScene(t, tied, 0x70, 4, 0)
	writeScene(t, tied, 0x55, 4, 0)
	if got := mustResume(t, tied); got != 0x55 {
		t.Fatalf("resumed scene %x, want the smaller id on a full tie", got)
	}

	// A domain with no scenes at all answers 0: that is the read's cue to open the first.
	empty := newTestEngine(t)
	got, err := scene.CurrentScene(empty, 7)
	if err != nil {
		t.Fatalf("CurrentScene on an empty domain: %v", err)
	}
	if got != 0 {
		t.Fatalf("an empty domain resumed scene %x, want 0", got)
	}
}

func mustResume(t *testing.T, engine *core.StorageEngine) uint64 {
	t.Helper()
	got, err := scene.CurrentScene(engine, agentForScene)
	if err != nil {
		t.Fatalf("CurrentScene: %v", err)
	}
	return got
}
