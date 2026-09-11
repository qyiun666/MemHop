// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/config"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// A rebuild is installed as soon as it is built, so an L1 failure cannot throw it
// away: the records it was computed from are already on disk, and the cache the
// domain keeps serving after a dropped rebuild reports topic depths and child
// links that no longer exist until a later pass succeeds.
func TestStructureStagesKeepsRebuildAcrossL1Failure(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "test.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	const (
		sceneID uint64 = 7
		topicID uint64 = 9
	)
	topic := func(depth uint8) *core.TopicSlot {
		return &core.TopicSlot{
			ID: topicID, SceneID: sceneID, Depth: depth,
			FusedKeywords: []string{"keep"}, UserTimestamp: 1, AgentTimestamp: 2,
		}
	}
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, topicID, topic(1)); err != nil {
		t.Fatalf("write topic: %v", err)
	}
	ac := domain.NewContext(core.DefaultAgentID, context.Background(), engine, nil, &config.MemHopDefaults{})

	// Sink the topic the way a landed compression would. Nothing in this package
	// mirrors that write, so the domain still serves the cache it was built with.
	if err := core.WriteTopicSlot(engine, core.DefaultAgentID, topicID, topic(2)); err != nil {
		t.Fatalf("sink topic: %v", err)
	}
	if got := ac.L2Meta.Get(topicID); got == nil || got.Depth != 1 {
		t.Fatalf("fixture wants a stale cache to read from, got %+v", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the next stage boundary exits, and the pipeline reports the exit
	if err := StructureStages(ctx, ac, core.DefaultAgentID, &core.DreamReport{}); err == nil {
		t.Fatal("a pipeline that exited at a stage boundary must report it")
	}
	got := ac.L2Meta.Get(topicID)
	if got == nil || got.Depth != 2 {
		t.Fatalf("rebuild dropped by an L1 failure: %+v", got)
	}
}
