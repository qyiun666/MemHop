// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 archive search/get surface tests.

package api

import (
	"testing"
)

func TestSurfaceL4Archive(t *testing.T) {
	db := openSurfaceDB(t)
	res, err := db.Search(SearchQuery{SceneName: "archive session"})
	if err != nil {
		t.Fatalf("seed search: %v", err)
	}
	topicID, err := db.Update(turnUpdate(res.Scene.SceneID, "archive me", "the archived reply"))
	if err != nil {
		t.Fatalf("seed update: %v", err)
	}
	byKeyword, err := db.SearchL4(L4Query{Keyword: "archived"})
	if err != nil {
		t.Fatalf("search l4 by keyword: %v", err)
	}
	if len(byKeyword) == 0 {
		t.Fatal("keyword search should find the reply archive")
	}
	byTopic, err := db.SearchL4(L4Query{Start: 1, End: 2_000_000_000_000, TopicID: &topicID})
	if err != nil {
		t.Fatalf("search l4 by topic: %v", err)
	}
	if len(byTopic) != 2 {
		t.Fatalf("topic archives = %d, want the turn's two originals", len(byTopic))
	}
	if _, err := db.SearchL4(L4Query{}); err != nil {
		t.Fatalf("empty l4 query must return empty set: %v", err)
	}
	got, err := db.GetArchive(byKeyword[0].IDHash)
	if err != nil || got == nil {
		t.Fatalf("get archive: %v", err)
	}
}
