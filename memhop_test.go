// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package memhop_test

import (
	"testing"

	memhop "github.com/qyiun666/MemHop"
)

func TestPublicIDHelpers(t *testing.T) {
	h := memhop.HashID("hello")
	if got := memhop.FormatHash(h); len(got) != 16 {
		t.Fatalf("FormatHash: %q", got)
	}
	parsed, err := memhop.ParseID(memhop.FormatHash(h))
	if err != nil {
		t.Fatal(err)
	}
	if parsed != h {
		t.Fatalf("ParseID roundtrip: got %d, want %d", parsed, h)
	}
	if _, err := memhop.ParseID("short"); err == nil {
		t.Fatal("short ID should fail")
	}

	ids := []uint64{1, 2, 3}
	formatted := memhop.FormatIDs(ids)
	parsedAll, ok := memhop.ParseAll(formatted)
	if !ok {
		t.Fatal("ParseAll should succeed")
	}
	if len(parsedAll) != len(ids) || parsedAll[1] != 2 {
		t.Fatalf("ParseAll roundtrip: %v", parsedAll)
	}
	if _, ok := memhop.ParseAll([]string{"bad"}); ok {
		t.Fatal("ParseAll should reject malformed IDs")
	}
}
