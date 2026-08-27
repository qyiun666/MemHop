// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func TestDistillL0Entry(t *testing.T) {
	db := newTestDB(t, newTestEngine(t))

	// No samples yet: the entry succeeds without an LLM call (skip guard),
	// for both the DB and the Session entry points.
	if err := db.DistillL0(context.Background(), core.DefaultAgentID); err != nil {
		t.Fatalf("db entry on empty domain: %v", err)
	}
	sess, err := db.NewSession(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := sess.DistillL0(context.Background()); err != nil {
		t.Fatalf("session entry on empty domain: %v", err)
	}

	// A closed database must be rejected, not silently distilled.
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := sess.DistillL0(context.Background()); common.CodeOf(err) != common.ErrClosed {
		t.Fatalf("want ErrClosed after Close, got %v", err)
	}
}
