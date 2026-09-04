// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 archive search/get surface tests.

package api

import (
	"testing"
)

func TestSurfaceL4Archive(t *testing.T) {
	db := openSurfaceDB(t)
	res, err := db.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("seed search: %v", err)
	}
	topicID, err := db.Update(turnUpdate(res.Scene.SceneID, res.NewTopicID, "archive me", "the archived reply"))
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
	// An empty query names no condition, so it returns the domain's archives.
	all, err := db.SearchL4(L4Query{})
	if err != nil || len(all) < 2 {
		t.Fatalf("empty l4 query: got %d archives, err %v", len(all), err)
	}
	// A single archive is a by-ID query — there is no dedicated getter.
	got, err := db.SearchL4(L4Query{IDs: []string{byKeyword[0].IDHash}})
	if err != nil || len(got) != 1 || got[0].IDHash != byKeyword[0].IDHash {
		t.Fatalf("archive by id: %+v err %v", got, err)
	}
	if _, err := db.SearchL4(L4Query{IDs: []string{"nothex"}}); CodeOf(err) != ErrInvalidQuery {
		t.Fatalf("malformed archive id: want ErrInvalidQuery, got %v", err)
	}
}
