// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"context"
	"net/http"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// refineTestTopic builds one turn topic carrying nL4 L4 messages, mirroring
// a turn whose intermediate messages arrived via AppendL4Message. Returns the
// topic id string.
func refineTestTopic(t *testing.T, db *DB, nL4 int) string {
	t.Helper()
	const sceneID = uint64(7)
	mustWriteScene(t, db.engine, core.DefaultAgentID, sceneID, "refine-scene")

	topicID := core.ComputeTurnTopicID(sceneID, 1000, 2000)
	if !repo.CreateTurnTopicL2(db.engine, core.DefaultAgentID, sceneID, topicID, []string{"turn-kw"}, 1000, 2000) {
		t.Fatal("create topic")
	}
	ids := make([]uint64, 0, nL4)
	for i := range nL4 {
		role := core.RoleUser
		if i == nL4-1 {
			role = core.RoleAgent
		}
		id, err := repo.AppendArchiveL4(db.engine, core.DefaultAgentID, topicID, role, core.ContentText,
			"msg-"+string(rune('A'+i)), int64(1000+i))
		if err != nil {
			t.Fatalf("append l4: %v", err)
		}
		ids = append(ids, id)
	}
	if !repo.UpdateTopicL4RefsL2(db.engine, core.DefaultAgentID, topicID, ids) {
		t.Fatal("update l4 refs")
	}
	return common.FormatHash(topicID)
}

// Refine replaces the single keyword track by re-distilling every original;
// depth and timestamps are untouched.
func TestRefineTopicKeywords(t *testing.T) {
	srv := mockLLMServer(t, `{"keywords":["fused1","fused2"]}`)
	db := newSearchTestDB(t, srv.URL)
	topicID := refineTestTopic(t, db, 3)

	if err := db.RefineTopicKeywords(context.Background(), core.DefaultAgentID, topicID); err != nil {
		t.Fatalf("RefineTopicKeywords: %v", err)
	}
	parsedID, err := common.ParseID(topicID)
	if err != nil {
		t.Fatalf("parse id: %v", err)
	}
	topic, err := core.ReadTopicLenient(db.engine, core.DefaultAgentID, parsedID)
	if err != nil || topic == nil {
		t.Fatalf("read topic: %v", err)
	}
	if len(topic.FusedKeywords) != 2 || topic.FusedKeywords[0] != "fused1" || topic.FusedKeywords[1] != "fused2" {
		t.Errorf("FusedKeywords = %v, want [fused1 fused2]", topic.FusedKeywords)
	}
	if topic.UserTimestamp != 1000 || topic.AgentTimestamp != 2000 {
		t.Errorf("timestamps not preserved: %+v", topic)
	}
	if topic.Depth != 1 {
		t.Errorf("Depth = %d, want 1", topic.Depth)
	}
	if arcs := repo.QueryArchiveL4(db.engine, core.DefaultAgentID, 3, "", 0, 0, topic.L4Refs); len(arcs) != 3 {
		t.Errorf("archives = %d, want 3", len(arcs))
	}
}

// There is no guard any more: refining always costs one LLM call, whatever
// the topic holds, and an empty L4 set is the only no-op.
func TestRefineAlwaysReDistills(t *testing.T) {
	srv, calls := countingLLMServer(t, `{"keywords":["fused1"]}`)
	db := newSearchTestDB(t, srv.URL)
	topicID := refineTestTopic(t, db, 2)

	if err := db.RefineTopicKeywords(context.Background(), core.DefaultAgentID, topicID); err != nil {
		t.Fatalf("first refine: %v", err)
	}
	if err := db.RefineTopicKeywords(context.Background(), core.DefaultAgentID, topicID); err != nil {
		t.Fatalf("second refine: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("LLM called %d times, want 2 (one per refine)", got)
	}
}

func TestRefineSkipsTopicWithoutOriginals(t *testing.T) {
	srv, calls := countingLLMServer(t, `{"keywords":["fused1"]}`)
	db := newSearchTestDB(t, srv.URL)
	const sceneID = uint64(7)
	mustWriteScene(t, db.engine, core.DefaultAgentID, sceneID, "s")
	topicID := core.ComputeTurnTopicID(sceneID, 1000, 2000)
	writeTopic(t, db.engine, core.DefaultAgentID, core.TopicSlot{
		ID: topicID, SceneID: sceneID, Depth: 1,
		FusedKeywords: []string{"keep"}, UserTimestamp: 1000, AgentTimestamp: 2000,
	})

	if err := db.RefineTopicKeywords(context.Background(), core.DefaultAgentID, common.FormatHash(topicID)); err != nil {
		t.Fatalf("refine with no originals: %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("LLM called %d times with nothing to distill", got)
	}
	topic, _ := core.ReadTopicLenient(db.engine, core.DefaultAgentID, topicID)
	if len(topic.FusedKeywords) != 1 || topic.FusedKeywords[0] != "keep" {
		t.Fatalf("keywords changed: %v", topic.FusedKeywords)
	}
}

// The failure paths must leave the topic exactly as it was.
func TestRefineTopicKeywordsErrors(t *testing.T) {
	t.Run("missing topic", func(t *testing.T) {
		srv := mockLLMServer(t, `{"keywords":["x"]}`)
		db := newSearchTestDB(t, srv.URL)
		err := db.RefineTopicKeywords(context.Background(), core.DefaultAgentID, "deadbeefdeadbeef")
		if common.CodeOf(err) != common.ErrNotFound {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("llm failure", func(t *testing.T) {
		srv := failingLLMServer(t, http.StatusBadRequest)
		db := newSearchTestDB(t, srv.URL)
		topicID := refineTestTopic(t, db, 3)
		err := db.RefineTopicKeywords(context.Background(), core.DefaultAgentID, topicID)
		if common.CodeOf(err) != common.ErrLLM {
			t.Fatalf("err = %v, want ErrLLM", err)
		}
		assertKeywordsUnchanged(t, db, topicID, "turn-kw")
	})
	t.Run("empty extraction", func(t *testing.T) {
		srv := mockLLMServer(t, `{"keywords":[]}`)
		db := newSearchTestDB(t, srv.URL)
		topicID := refineTestTopic(t, db, 3)
		err := db.RefineTopicKeywords(context.Background(), core.DefaultAgentID, topicID)
		if common.CodeOf(err) != common.ErrLLM {
			t.Fatalf("err = %v, want ErrLLM", err)
		}
		assertKeywordsUnchanged(t, db, topicID, "turn-kw")
	})
}

func assertKeywordsUnchanged(t *testing.T, db *DB, topicIDHex string, want string) {
	t.Helper()
	parsedID, err := common.ParseID(topicIDHex)
	if err != nil {
		t.Fatalf("parse id: %v", err)
	}
	topic, err := core.ReadTopicLenient(db.engine, core.DefaultAgentID, parsedID)
	if err != nil || topic == nil {
		t.Fatalf("read topic: %v", err)
	}
	if len(topic.FusedKeywords) != 1 || topic.FusedKeywords[0] != want {
		t.Fatalf("topic keywords changed on failure: %v", topic.FusedKeywords)
	}
}
