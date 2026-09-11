// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dream

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/qyiun666/MemHop/internal/config"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// A tree whose in-flight step will not read back is not a finished tree: the
// sweep's exemption is computed from every node of the tree, so the one node that
// would hold it alive is exactly the one that can go missing. The stale step stays
// until a pass that sees the whole tree runs.
func TestPrunePlanStageSkipsWhenTheTreeIsIncomplete(t *testing.T) {
	engine, err := core.Create(filepath.Join(t.TempDir(), "test.meh"))
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	slog.SetLogLoggerLevel(slog.LevelError)

	const topicID uint64 = 9
	staleID := core.HashPlanNode(topicID, 1)
	liveID := core.HashPlanNode(topicID, 2)
	stale := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
	for _, n := range []*core.PlanNode{
		{IDHash: staleID, TopicID: topicID, Seq: 1, Status: core.StatusDone, UpdatedAt: stale},
		{IDHash: liveID, TopicID: topicID, Seq: 2, ParentSeq: 1, Status: core.StatusInProgress, UpdatedAt: time.Now().UnixMilli()},
	} {
		if _, err := repo.WritePlanNode(engine, core.DefaultAgentID, n); err != nil {
			t.Fatalf("write node %d: %v", n.Seq, err)
		}
	}
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL5PlanNode, liveID, []byte(`{"id":`)); err != nil {
		t.Fatalf("make step 2 unreadable: %v", err)
	}

	ac := domain.NewContext(core.DefaultAgentID, context.Background(), engine, nil, &config.MemHopDefaults{})
	rep := &core.DreamReport{}
	PrunePlanStage(ac, core.DefaultAgentID, rep)

	if _, err := core.ReadPlanNode(engine, core.DefaultAgentID, staleID); err != nil {
		t.Fatalf("the sweep ran on a tree it could not see whole: %v", err)
	}
	if stageStatusOf(rep, "l5_prune") != "error" {
		t.Fatalf("an unswept stage must report its cause, got %+v", rep.Stages)
	}
}

func stageStatusOf(rep *core.DreamReport, name string) string {
	for _, s := range rep.Stages {
		if s.Name == name {
			return s.Status
		}
	}
	return ""
}
