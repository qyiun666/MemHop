// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/config"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/internal/repo/index"
)

// declining answers every prompt as "no groups": a model that sees nothing worth merging.
type declining struct {
	mu    sync.Mutex
	asks  int
	limit int
}

func (d *declining) Chat(_ context.Context, _ string, _ string, _ int) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.asks++
	return `{"l2_groups":[]}`, nil
}

func (d *declining) ChatWithRetry(_ context.Context, system, user string, _, _ int) (string, error) {
	return d.Chat(context.Background(), system, user, 0)
}

func (d *declining) MaxOutputTokens() int { return 2048 }

func (d *declining) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.asks
}

// A scene the model will not compress stays above the trigger, so the next pass asks again:
// the engine keeps no memory of a refusal. What one such pass costs is exactly one
// consolidation call per scene — measured here rather than assumed, because the host's
// answer to "why did memory spend a call while nothing changed" is this number.
func TestDeclinedConsolidationAsksOncePerScenePerPass(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "test.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	const sceneID = uint64(7)
	stub := &declining{}
	ac := domain.NewContext(core.DefaultAgentID, context.Background(), engine, stub, &config.MemHopDefaults{})
	base := time.Now().Add(-time.Hour)
	for i, id := range []uint64{11, 12, 13, 14, 15} {
		at := base.Add(time.Duration(i) * time.Minute)
		topic := core.TopicSlot{
			ID: id, SceneID: sceneID, Depth: 1, FusedKeywords: []string{"kw"},
			UserTimestamp: at.UnixMilli(), AgentTimestamp: at.UnixMilli(),
		}
		if err := core.WriteTopicSlot(engine, core.DefaultAgentID, id, &topic); err != nil {
			t.Fatalf("write topic %d: %v", id, err)
		}
	}
	ac.L2Meta = index.BuildL2MetaFromEngine(engine, core.DefaultAgentID)
	surfaceOf := func() int {
		return len(repo.ListTopicsL2(repo.TopicListQuery{MetaIdx: ac.L2Meta, SceneID: sceneID, Depth: 1}))
	}
	before := surfaceOf()
	if before != 5 {
		t.Fatalf("scene starts with %d surface topics, want 5", before)
	}

	for pass := 1; pass <= 3; pass++ {
		if _, _, err := CompressScenes(context.Background(), ac, []uint64{sceneID}, &core.DreamReport{}); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if got := stub.count(); got != pass {
			t.Fatalf("after %d passes the model was asked %d times, want one call per scene per pass", pass, got)
		}
		if got := surfaceOf(); got != before {
			t.Fatalf("a declined pass moved the surface: %d rows, want %d", got, before)
		}
	}
}
