// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package scene

import (
	"context"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/domain"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func newTestContext(t *testing.T, engine *core.StorageEngine) *domain.Context {
	t.Helper()
	return domain.NewContext(core.DefaultAgentID, context.Background(), engine, nil, nil)
}

// A topic's archives are found through the domain's archive index, so the one
// failure the read must report is the mirror disagreeing with the disk: an
// entry naming a record that is gone. Silently returning the shorter transcript
// would look exactly like a turn that never said that line.
func TestContextTopicIndexDriftIsAnError(t *testing.T) {
	engine := newTestEngine(t)
	ac := newTestContext(t, engine)
	const topicID uint64 = 7
	if _, err := repo.AppendArchiveL4(engine, core.DefaultAgentID, ac.Arch, repo.ArchiveContent{
		TopicID: topicID, Role: core.RoleUser, Type: core.ContentText, Text: "原文", CreatedAt: 1000,
	}); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	// Delete the record behind the index's back: this is mirror drift, not a
	// retired reference (a topic owns nothing it has not been written under).
	var gone []uint64
	for _, arc := range core.CollectAllArchives(engine, core.DefaultAgentID) {
		gone = append(gone, arc.IDHash)
	}
	if _, err := engine.DeleteRecordBatch(core.DefaultAgentID, gone); err != nil {
		t.Fatalf("delete record: %v", err)
	}
	if _, err := ContextTopic(ac, core.DefaultAgentID, core.TopicSlot{ID: topicID, Depth: 1}, nil); common.CodeOf(err) != common.ErrIO {
		t.Fatalf("index naming a missing record must be ErrIO, got %v", err)
	}
}

// A topic nobody ever wrote content under reads back with no messages and no
// error: that is an unsettled (or fully expired) turn, not a broken one.
func TestContextTopicWithoutArchivesIsEmpty(t *testing.T) {
	engine := newTestEngine(t)
	ac := newTestContext(t, engine)
	st, err := ContextTopic(ac, core.DefaultAgentID, core.TopicSlot{ID: 0xbeef, Depth: 1}, nil)
	if err != nil {
		t.Fatalf("a topic owning nothing must read empty, not fail: %v", err)
	}
	if len(st.Messages) != 0 {
		t.Fatalf("want no messages, got %+v", st.Messages)
	}
}

// A file written while topics still carried lists of their archives keeps that
// `l4_refs` field, and nothing has to migrate: the field is ignored on decode
// while every archive already names the topic it belongs to. The fixture points
// the retired list at an id that exists nowhere — so content coming back means
// the read addressed it by the topic key. This is why no format version moved.
func TestContextTopicReadsARefCarryingRecord(t *testing.T) {
	engine := newTestEngine(t)
	const topicID uint64 = 0x1234
	raw := `{"id":4660,"scene_id":99,"depth":1,"fused_keywords":["rust"],"l4_refs":[987654321]}`
	if _, err := engine.WriteRecord(core.DefaultAgentID, core.RecL2Topic, topicID, []byte(raw)); err != nil {
		t.Fatalf("write topic record: %v", err)
	}
	arcID := common.HashID("legacy:line")
	if err := core.WriteArchiveSlot(engine, core.DefaultAgentID, arcID, &core.ArchiveSlot{
		IDHash: arcID, ContextID: topicID, Content: "旧记录里的一轮", CreatedAt: 1000,
	}); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	ac := newTestContext(t, engine)
	st, err := ContextTopic(ac, core.DefaultAgentID, core.TopicSlot{ID: topicID, Depth: 1}, nil)
	if err != nil {
		t.Fatalf("read a ref-carrying topic: %v", err)
	}
	if len(st.Messages) != 1 || st.Messages[0].Content != "旧记录里的一轮" {
		t.Fatalf("content must come from the topic key, not the retired list: %+v", st.Messages)
	}
}
