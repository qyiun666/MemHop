// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// writeArchive writes an L4 archive record.
func writeArchive(t *testing.T, engine *core.StorageEngine, arc *core.ArchiveSlot) {
	t.Helper()
	if err := core.WriteArchiveSlot(engine, core.DefaultAgentID, arc.IDHash, arc); err != nil {
		t.Fatalf("write archive: %v", err)
	}
}

// TestSearchL4ByID reads archives by ID; a missing ID is simply skipped.
func TestSearchL4ByID(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	topicHash := common.HashID("topic1")
	a1 := core.ArchiveSlot{IDHash: common.HashID("m1"), ContextID: topicHash, Content: "hello", CreatedAt: 1000, Role: 0, ContentType: core.ContentText}
	writeArchive(t, engine, &a1)

	got, err := db.SearchL4(core.DefaultAgentID, L4Query{IDs: []string{common.FormatHash(a1.IDHash)}})
	if err != nil {
		t.Fatalf("SearchL4 by id: %v", err)
	}
	if len(got) != 1 || got[0].Content != "hello" || got[0].ContextID != topicHash {
		t.Fatalf("unexpected archive: %+v", got)
	}

	miss, err := db.SearchL4(core.DefaultAgentID, L4Query{IDs: []string{common.FormatHash(12345)}})
	if err != nil || len(miss) != 0 {
		t.Fatalf("missing id: want empty result, got %v / %v", miss, err)
	}
	if _, err := db.SearchL4(core.DefaultAgentID, L4Query{IDs: []string{"nothex"}}); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("malformed id: want ErrInvalidQuery, got %v", err)
	}
}

// TestSearchL4TopicOnly pins the read a host needs after a turn: naming only
// the topic (or only the content type) must resolve that turn's originals
// instead of falling through to an empty result.
func TestSearchL4TopicOnly(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	t1, t2 := common.HashID("only1"), common.HashID("only2")
	writeArchive(t, engine, &core.ArchiveSlot{IDHash: common.HashID("o1"), ContextID: t1, Content: "u", CreatedAt: 1000})
	writeArchive(t, engine, &core.ArchiveSlot{IDHash: common.HashID("o2"), ContextID: t1, Content: "a", CreatedAt: 1001})
	writeArchive(t, engine, &core.ArchiveSlot{IDHash: common.HashID("o3"), ContextID: t2, Content: "other", CreatedAt: 1002})

	t1Hex := common.FormatHash(t1)
	got, err := db.SearchL4(core.DefaultAgentID, L4Query{TopicID: &t1Hex})
	if err != nil {
		t.Fatalf("topic-only: %v", err)
	}
	if len(got) != 2 || got[0].IDHash != common.HashID("o1") || got[1].IDHash != common.HashID("o2") {
		t.Fatalf("topic-only: want the two o1/o2 archives in time order, got %+v", got)
	}

	all, err := db.SearchL4(core.DefaultAgentID, L4Query{})
	if err != nil || len(all) != 3 {
		t.Fatalf("empty query: want every archive, got %d / %v", len(all), err)
	}
}

// TestSearchL4TopicFilter three modes combined with TopicID filtering.
func TestSearchL4TopicFilter(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	t1, t2 := common.HashID("t1"), common.HashID("t2")
	a1 := core.ArchiveSlot{IDHash: common.HashID("m1"), ContextID: t1, Content: "rust 所有权", CreatedAt: 1000, Role: 0, ContentType: core.ContentText}
	a2 := core.ArchiveSlot{IDHash: common.HashID("m2"), ContextID: t1, Content: "生命周期", CreatedAt: 2000, Role: 1, ContentType: core.ContentText}
	a3 := core.ArchiveSlot{IDHash: common.HashID("m3"), ContextID: t2, Content: "rust 生态", CreatedAt: 3000, Role: 0, ContentType: core.ContentText}
	writeArchive(t, engine, &a1)
	writeArchive(t, engine, &a2)
	writeArchive(t, engine, &a3)
	t1Hex, t2Hex := common.FormatHash(t1), common.FormatHash(t2)

	// Keyword + TopicID: a1 hits (a3 belongs to t2 and is excluded).
	out, err := db.SearchL4(core.DefaultAgentID, L4Query{Keyword: "rust", TopicID: &t1Hex})
	if err != nil {
		t.Fatalf("SearchL4 keyword+topic: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != a1.IDHash {
		t.Fatalf("keyword+topic: want [m1], got %v", out)
	}

	// Time range + TopicID: a1 only (Start must be > 0; 0 means unset).
	out, err = db.SearchL4(core.DefaultAgentID, L4Query{Start: 500, End: 1500, TopicID: &t1Hex})
	if err != nil {
		t.Fatalf("SearchL4 range+topic: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != a1.IDHash {
		t.Fatalf("range+topic: want [m1], got %v", out)
	}

	// IDs mode + TopicID: only a3.
	out, err = db.SearchL4(core.DefaultAgentID, L4Query{IDs: []string{common.FormatHash(a1.IDHash), common.FormatHash(a2.IDHash), common.FormatHash(a3.IDHash)}, TopicID: &t2Hex})
	if err != nil {
		t.Fatalf("SearchL4 ids+topic: %v", err)
	}
	if len(out) != 1 || out[0].IDHash != a3.IDHash {
		t.Fatalf("ids+topic: want [m3], got %v", out)
	}

	// Invalid TopicID errors.
	if _, err := db.SearchL4(core.DefaultAgentID, L4Query{Keyword: "rust", TopicID: new("nothex")}); err == nil {
		t.Fatal("want error for invalid topic id")
	}
}

// TestSearchL4TypeFilter: the optional content-type filter narrows results
// within the query modes.
func TestSearchL4TypeFilter(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	topic := common.HashID("types")
	text := core.ArchiveSlot{IDHash: common.HashID("m-text"), ContextID: topic, Content: "文字内容", CreatedAt: 2000, ContentType: core.ContentText}
	image := core.ArchiveSlot{IDHash: common.HashID("m-img"), ContextID: topic, Content: "img://cat.png", CreatedAt: 3000, ContentType: core.ContentImage}
	writeArchive(t, engine, &text)
	writeArchive(t, engine, &image)

	img := core.ContentImage
	got, err := db.SearchL4(core.DefaultAgentID, L4Query{Start: 1000, End: 4000, Type: &img})
	if err != nil {
		t.Fatalf("SearchL4: %v", err)
	}
	if len(got) != 1 || got[0].ContentType != core.ContentImage {
		t.Fatalf("type filter: %+v, want only the image archive", got)
	}
}

// The archive keyword is matched case-insensitively, like the L3 node keyword:
// a host that types "RUST" into either layer gets the same set back. Limit caps
// the result from the newest end, because the read is ordered oldest first.
func TestSearchL4KeywordCaseAndLimit(t *testing.T) {
	engine := newTestEngine(t)
	db := newTestDB(t, engine)
	topic := common.HashID("case")
	old := core.ArchiveSlot{IDHash: common.HashID("m-old"), ContextID: topic, Content: "Rust 所有权", CreatedAt: 1000}
	fresh := core.ArchiveSlot{IDHash: common.HashID("m-new"), ContextID: topic, Content: "rust 生态", CreatedAt: 2000}
	other := core.ArchiveSlot{IDHash: common.HashID("m-other"), ContextID: topic, Content: "go 并发", CreatedAt: 3000}
	for _, a := range []core.ArchiveSlot{old, fresh, other} {
		writeArchive(t, engine, &a)
	}

	for _, kw := range []string{"RUST", "rust", "Rust"} {
		got, err := db.SearchL4(core.DefaultAgentID, L4Query{Keyword: kw})
		if err != nil {
			t.Fatalf("keyword %q: %v", kw, err)
		}
		if len(got) != 2 {
			t.Fatalf("keyword %q: want both rust archives, got %+v", kw, got)
		}
	}

	capped, err := db.SearchL4(core.DefaultAgentID, L4Query{Keyword: "rust", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 1 || capped[0].IDHash != fresh.IDHash {
		t.Fatalf("limit 1: want the newest match only, got %+v", capped)
	}
	if all, err := db.SearchL4(core.DefaultAgentID, L4Query{Limit: 2}); err != nil || len(all) != 2 || all[0].IDHash != fresh.IDHash {
		t.Fatalf("limit over an unfiltered read: %+v / %v", all, err)
	}
}
