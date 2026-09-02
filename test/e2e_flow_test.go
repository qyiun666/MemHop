// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build integration

package test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// TestE2EUpdateDream exercises the full memory loop against a real LLM:
// open the host session → settle one turn → read the session back → L4
// archive readback → Dream on that session → readable afterwards.
func TestE2EUpdateDream(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()

	ts := time.Now().UnixMilli()
	userText := "我喜欢在周末去海边跑步，尤其是清晨人少的时候"
	agentText := "海边晨跑很不错，空气清新还能看日出，记得做好防晒"

	// 1. Opening a fresh session hands back its empty surface and the topic id
	// of the turn it just opened.
	res, err := db.Search(memhop.SearchQuery{})
	if err != nil {
		t.Fatalf("Search(fresh): %v", err)
	}
	sceneID := res.Scene.SceneID
	if len(res.Topics) != 0 {
		t.Fatalf("fresh session returned %d topics", len(res.Topics))
	}
	if res.NewTopicID == "" {
		t.Fatal("the opening read must issue the turn's topic id")
	}

	// 2. Update settles the turn into that topic: one topic, both originals,
	// distilled keywords.
	topicID, err := db.Update(memhop.TurnUpdate{
		SceneID: sceneID, TopicID: res.NewTopicID, UserText: userText, UserTS: ts,
		AgentText: agentText, AgentTS: ts + 1000,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// 3. The session read hands the turn back.
	res2, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search(session): %v", err)
	}
	if len(res2.Topics) != 1 || res2.Topics[0].ID != topicID {
		t.Fatalf("surface = %+v, want the one turn %s", res2.Topics, topicID)
	}
	if len(res2.Topics[0].FusedKeywords) == 0 {
		t.Fatal("the turn topic carries no keywords")
	}
	t.Logf("topic %s keywords=%v", topicID, res2.Topics[0].FusedKeywords)

	// 4. L4 archive readback: TopicID is an overlay filter, so combine it with
	//    a primary mode (time range) to select the topic's archives.
	archives, err := db.SearchL4(internal.L4Query{
		Start:   ts - 1000,
		End:     ts + 5000,
		TopicID: &topicID,
	})
	if err != nil {
		t.Fatalf("SearchL4: %v", err)
	}
	if len(archives) != 2 {
		t.Fatalf("expected 2 archives for the turn, got %d", len(archives))
	}
	for _, a := range archives {
		if a.Content != userText && a.Content != agentText {
			t.Errorf("archive content is not an original: %.40s", a.Content)
		}
	}

	// 5. Dream on this session (L2 compression + L1 rebuild/decay).
	rep, err := db.Dream(context.Background(), sceneID)
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if rep == nil {
		t.Fatal("Dream returned nil report")
	}
	t.Logf("dream report: consolidated=%d compressed=%d stages=%d", rep.ConsolidatedScenes, rep.L2TopicsCompressed, len(rep.Stages))

	// 6. After Dream the session must still read back.
	res3, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("Search after Dream: %v", err)
	}
	t.Logf("post-dream session has %d surface topic(s)", len(res3.Topics))
}

// TestE2ECapability covers L5 capability import (path) / query / delete
// against a live DB.
func TestE2ECapability(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "morning_run.json")
	content := `{"format":"memhop-capability/v3","name":"晨跑流程","version":"1","type":"skill","summary":"周末海边晨跑","trigger":"用户提到周末海边跑步","resources":[{"type":"skill","name":"晨跑计划","desc":"周末清晨海边跑步","input":"{\"type\":\"object\",\"properties\":{\"time\":{\"type\":\"string\"}}}","output":"晨跑计划"}]}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write import file: %v", err)
	}
	cap, err := db.ImportCapability(path)
	if err != nil {
		t.Fatalf("ImportCapability: %v", err)
	}
	if cap == nil || cap.IDHash == "" {
		t.Fatal("ImportCapability returned empty capability")
	}
	id := cap.IDHash

	got, err := db.GetCapability(id)
	if err != nil {
		t.Fatalf("GetCapability(%s): %v", id, err)
	}
	if got.Name != "晨跑流程" || got.Type != core.CapabilitySkill {
		t.Fatalf("unexpected capability: %+v", got)
	}
	if len(got.Resources) != 1 || got.Resources[0].Name != "晨跑计划" {
		t.Fatalf("resources mismatch: %+v", got.Resources)
	}

	caps, err := db.ListCapabilities(internal.CapabilityListQuery{Keyword: "晨跑"})
	if err != nil {
		t.Fatalf("ListCapabilities: %v", err)
	}
	if len(caps) == 0 {
		t.Fatal("ListCapabilities(Keyword=晨跑) returned no capabilities")
	}

	if err := db.DeleteCapability(id); err != nil {
		t.Fatalf("DeleteCapability(%s): %v", id, err)
	}
	if _, err := db.GetCapability(id); err == nil {
		t.Fatal("GetCapability after DeleteCapability should fail")
	}
}

// TestE2EL0Profile covers L0 profile read/update round-trip.
func TestE2EL0Profile(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()

	// Fresh DB: GetL0 returns an empty profile (no ErrNotFound).
	slot, err := db.GetL0()
	if err != nil {
		t.Fatalf("GetL0: %v", err)
	}
	slot.Personality = "热爱户外运动的用户"
	if err := db.UpdateL0(slot); err != nil {
		t.Fatalf("UpdateL0: %v", err)
	}
	got, err := db.GetL0()
	if err != nil {
		t.Fatalf("GetL0 after update: %v", err)
	}
	if got.Personality != "热爱户外运动的用户" {
		t.Fatalf("Personality mismatch: %q", got.Personality)
	}
}
