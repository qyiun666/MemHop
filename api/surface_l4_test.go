// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L4 archive search/get surface tests.

package api

import (
	"context"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

func TestSurfaceL4Archive(t *testing.T) {
	db, _ := openSurfaceDB(t)
	ctx := context.Background()
	res, err := db.Search(ctx, SearchQuery{Text: "archive me", AutoCreate: true, Timestamp: 1_700_000_030_000})
	if err != nil {
		t.Fatalf("seed search: %v", err)
	}
	if err := db.Update(common.FormatHash(res.NewTopicID), "the archived reply", 1_700_000_030_500); err != nil {
		t.Fatalf("seed update: %v", err)
	}
	byKeyword, err := db.SearchL4(L4Query{Keyword: "archived"})
	if err != nil {
		t.Fatalf("search l4 by keyword: %v", err)
	}
	if len(byKeyword) == 0 {
		t.Fatal("keyword search should find the reply archive")
	}
	if _, err := db.SearchL4(L4Query{}); err != nil {
		t.Fatalf("empty l4 query must return empty set: %v", err)
	}
	got, err := db.GetArchive(common.FormatHash(byKeyword[0].IDHash))
	if err != nil || got == nil {
		t.Fatalf("get archive: %v", err)
	}
}
