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

	internal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// TestE2ESearchUpdateDream exercises the full memory loop against real
// services: Search (auto-create) → Update (agent reply) → Search again →
// Dream (consolidation) → L4 archive readback.
func TestE2ESearchUpdateDream(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()

	ts := time.Now().UnixMilli()
	userText := "我喜欢在周末去海边跑步，尤其是清晨人少的时候"

	// 1. Search with AutoCreate: no match expected on a fresh DB, so this
	//    creates a new scene + topic and returns it.
	res, err := db.Search(context.Background(), internal.SearchQuery{
		Text:       userText,
		AutoCreate: true,
		Timestamp:  ts,
	})
	if err != nil {
		t.Fatalf("Search(AutoCreate): %v", err)
	}
	if len(res.Contexts) == 0 {
		t.Fatal("Search(AutoCreate) returned no contexts")
	}
	if res.NewTopicID == 0 {
		t.Fatal("Search(AutoCreate) should set NewTopicID")
	}
	topicID := common.FormatHash(res.Contexts[0].ID)
	t.Logf("created topic ID=%s NewTopicID=%d", topicID, res.NewTopicID)

	// 2. Update: append the agent reply to the topic.
	agentText := "海边晨跑很不错，空气清新还能看日出，记得做好防晒"
	if err := db.Update(topicID, agentText, ts+1000); err != nil {
		t.Fatalf("Update(topicID=%s) failed: %v", topicID, err)
	}

	// 3. Search again (normal route): should hit the existing scene.
	res2, err := db.Search(context.Background(), internal.SearchQuery{
		Text:      "周末海边跑步",
		Timestamp: ts + 2000,
	})
	if err != nil {
		t.Fatalf("Search(normal): %v", err)
	}
	if len(res2.Contexts) == 0 {
		t.Fatal("Search(normal) returned no contexts after data written")
	}
	t.Logf("normal search hit %d contexts, %d associated",
		len(res2.Contexts), len(res2.AssociatedContexts))

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
	if len(archives) < 2 {
		t.Fatalf("expected >=2 archives for topic, got %d", len(archives))
	}
	t.Logf("L4 archives for topic: %d", len(archives))

	// 5. Dream: consolidate active scenes (L2 compression + L1 rebuild/decay).
	ok, err := db.Dream(context.Background(), "")
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	if !ok {
		t.Fatal("Dream returned ok=false")
	}

	// 6. After dream the DB must still be readable.
	res3, err := db.Search(context.Background(), internal.SearchQuery{
		Text:      "海边",
		Timestamp: ts + 3000,
	})
	if err != nil {
		t.Fatalf("Search after Dream: %v", err)
	}
	t.Logf("post-dream search returned %d contexts", len(res3.Contexts))
}

// TestE2ECapability covers L5 capability import (path) / query / delete
// against a live DB.
func TestE2ECapability(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "morning_run.json")
	content := `{"format":"memhop-capability/v2","name":"晨跑流程","version":"1","type":"skill","summary":"周末海边晨跑","trigger":"用户提到周末海边跑步","resources":[{"type":"skill","name":"晨跑计划","description":"周末清晨海边跑步"}]}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write import file: %v", err)
	}
	cap, err := db.ImportCapability(path)
	if err != nil {
		t.Fatalf("ImportCapability: %v", err)
	}
	if cap == nil || cap.IDHash == 0 {
		t.Fatal("ImportCapability returned empty capability")
	}
	id := common.FormatHash(cap.IDHash)

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
