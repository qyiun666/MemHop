// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/config"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// The plan stage's exemption has two conditions, and the guides advertise only one of them.
// Both matter because they fail in opposite directions: dropping "in flight" would delete the
// tree a running task is still walking, while dropping "active inside the window" would let any
// abandoned task hold its tree forever and make L5 unbounded - the reason the stage exists.
// Inside a tree that is not exempt, each node is then measured on its own clock, so a finished
// plan that one late update touched loses only the stale steps.
func TestPrunePlanStageNeedsBothConditionsToSpareATree(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "prune.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	defaults := config.DefaultMemHopDefaults
	defaults.ContentRetentionMs = 1000
	ac := domain.NewContext(core.DefaultAgentID, context.Background(), engine, &declining{}, &defaults)

	now := time.Now().UnixMilli()
	stale := now - 60_000
	write := func(topicID uint64, seq uint32, status uint8, at int64) {
		id := core.HashPlanNode(topicID, seq)
		if err := repo.WritePlanNode(engine, core.DefaultAgentID, &core.PlanNode{
			IDHash: id, TopicID: topicID, Seq: seq, Status: status, UpdatedAt: at,
		}); err != nil {
			t.Fatalf("write plan node: %v", err)
		}
	}
	const (
		inFlightRecent = uint64(0x1111)
		inFlightSilent = uint64(0x2222)
		finishedMixed  = uint64(0x3333)
	)
	write(inFlightRecent, 1, core.StatusInProgress, now)
	write(inFlightSilent, 1, core.StatusInProgress, stale)
	write(finishedMixed, 1, core.StatusDone, stale)
	write(finishedMixed, 2, core.StatusDone, now)

	PrunePlanStage(ac, core.DefaultAgentID, &core.DreamReport{})

	nodes, err := core.CollectAllPlanNodesStrict(engine, core.DefaultAgentID)
	if err != nil {
		t.Fatalf("collect after the sweep: %v", err)
	}
	left := map[uint64]int{}
	for _, n := range nodes {
		left[n.TopicID]++
	}
	if left[inFlightRecent] != 1 {
		t.Errorf("an in-flight tree that acted inside the window lost its steps: %+v (topic %s)",
			left, common.FormatHash(inFlightRecent))
	}
	if left[inFlightSilent] != 0 {
		t.Errorf("an in-flight but silent tree survived the window, so L5 has no bound: %+v (topic %s)",
			left, common.FormatHash(inFlightSilent))
	}
	if left[finishedMixed] != 1 {
		t.Errorf("a finished tree should lose only the stale step, keeping the one touched inside the window: %+v (topic %s)",
			left, common.FormatHash(finishedMixed))
	}
}
