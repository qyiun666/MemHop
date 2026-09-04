// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package scene

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// TestContextTopicRetiredRefIsNotAnError pins the line between the two kinds
// of unreadable ref: a ref naming no record is reported in L4IDs with no
// message (an Update replay legally retires the archives of the turn it
// replaced), while a record that cannot be read must surface as an error.
func TestContextTopicRetiredRefIsNotAnError(t *testing.T) {
	engine := newTestEngine(t)
	alive := core.ArchiveSlot{IDHash: common.HashID("alive"), ContextID: 7, Content: "原文"}
	if err := core.WriteArchiveSlot(engine, core.DefaultAgentID, alive.IDHash, &alive); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	st, err := ContextTopic(engine, core.DefaultAgentID, core.TopicSlot{
		ID: 7, Depth: 1, L4Refs: []uint64{alive.IDHash, common.HashID("retired")},
	}, nil)
	if err != nil {
		t.Fatalf("a retired ref must not fail the read: %v", err)
	}
	if len(st.L4IDs) != 2 {
		t.Fatalf("both refs must be reported: %+v", st.L4IDs)
	}
	if len(st.Messages) != 1 || st.Messages[0].Content != "\u539f\u6587" {
		t.Fatalf("only the live ref carries a message: %+v", st.Messages)
	}
}
