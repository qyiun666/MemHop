// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestDreamReportContract(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))
	sess, err := db.NewSession(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	ctx := context.Background()

	// Nothing to consolidate: a zero-valued success report, no stages.
	rep, err := sess.Dream(ctx, "")
	if err != nil || rep == nil {
		t.Fatalf("dream on empty domain: rep=%v err=%v", rep, err)
	}
	if rep.ConsolidatedScenes != 0 || rep.L2TopicsCompressed != 0 || len(rep.Stages) != 0 {
		t.Fatalf("noop report = %+v, want zero counts and no stages", rep)
	}

	// Invalid scene id fails fast before any stage runs.
	rep, err = sess.Dream(ctx, "zz")
	if common.CodeOf(err) != common.ErrInvalidQuery || rep != nil {
		t.Fatalf("bad hex scene: rep=%v code=%d err=%v", rep, common.CodeOf(err), err)
	}
}
