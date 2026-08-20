// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package common

import (
	"strings"
	"testing"
)

func TestLevenshteinDistanceIntoMatchesLevenshteinDistance(t *testing.T) {
	cases := []struct{ a, b string }{
		{"", ""},
		{"", "abc"},
		{"abc", ""},
		{"a", ""},
		{"", "a"},
		{"a", "a"},
		{"a", "b"},
		{"kitten", "sitting"},
		{"sitting", "kitten"}, // symmetric
		{"abc", "abc"},
		{"abc", "abd"},
		{"flaw", "lawn"},
		{"intention", "execution"},
		{"中国", "中国人"},
		{"你好世界", "你好"},
		{"同一性", "同一性检查"},
	}
	var prev, curr []int // work buffers reused across all cases
	for i, tc := range cases {
		want := LevenshteinDistance(tc.a, tc.b)
		got, p, c := LevenshteinDistanceInto(tc.a, tc.b, prev, curr)
		prev, curr = p, c
		if got != want {
			t.Fatalf("case %d (%q, %q): got %d want %d", i, tc.a, tc.b, got, want)
		}
	}
}

func TestLevenshteinDistanceIntoReusesBuffers(t *testing.T) {
	prev := make([]int, 0, 64)
	curr := make([]int, 0, 64)
	want := LevenshteinDistance("kitten", "sitting")
	d, p, c := LevenshteinDistanceInto("kitten", "sitting", prev, curr)
	if d != want {
		t.Fatalf("got %d want %d", d, want)
	}
	if &p[:1][0] != &prev[:1][0] || &c[:1][0] != &curr[:1][0] {
		t.Fatal("buffers with sufficient capacity must be reused in place")
	}
	if cap(p) < len("sitting")+1 || cap(c) < len("sitting")+1 {
		t.Fatalf("work buffers not sized for the job: cap %d/%d", cap(p), cap(c))
	}
	// the returned buffers stay correct on a second call
	d2, p2, c2 := LevenshteinDistanceInto("flaw", "lawn", p, c)
	if d2 != LevenshteinDistance("flaw", "lawn") {
		t.Fatalf("second call got %d", d2)
	}
	if &p2[0] != &p[0] || &c2[0] != &c[0] {
		t.Fatal("second call should reuse the same backing arrays")
	}
}

func TestLevenshteinDistanceIntoGrowsBuffers(t *testing.T) {
	a := strings.Repeat("x", 64)
	b := strings.Repeat("y", 64)
	prev := make([]int, 0, 1)
	curr := make([]int, 0, 1)
	want := LevenshteinDistance(a, b)
	d, p, c := LevenshteinDistanceInto(a, b, prev, curr)
	if d != want {
		t.Fatalf("got %d want %d", d, want)
	}
	if cap(p) < len(b)+1 || cap(c) < len(b)+1 {
		t.Fatalf("grown buffers too small: cap %d/%d", cap(p), cap(c))
	}
	if &p[:1][0] == &prev[:1][0] || &c[:1][0] == &curr[:1][0] {
		t.Fatal("expected new buffers when capacity is insufficient")
	}
	// grown buffers handle a shorter string correctly
	d2, _, _ := LevenshteinDistanceInto("kitten", "sitting", p, c)
	if d2 != LevenshteinDistance("kitten", "sitting") {
		t.Fatalf("short string on grown buffers: got %d", d2)
	}
	// alternating row lengths also work: shrink after grow
	d3, _, _ := LevenshteinDistanceInto("同一性", "同一性检查", p, c)
	if d3 != LevenshteinDistance("同一性", "同一性检查") {
		t.Fatalf("unicode on grown buffers: got %d", d3)
	}
}

func TestLevenshteinDistanceIntoEmptyFastPaths(t *testing.T) {
	prev := make([]int, 3, 8)
	curr := make([]int, 3, 8)
	d, p, c := LevenshteinDistanceInto("", "abc", prev, curr)
	if d != 3 || &p[0] != &prev[0] || &c[0] != &curr[0] {
		t.Fatalf("empty a: got %d, buffers must pass through unchanged", d)
	}
	d, p, c = LevenshteinDistanceInto("abc", "", prev, curr)
	if d != 3 || &p[0] != &prev[0] || &c[0] != &curr[0] {
		t.Fatalf("empty b: got %d, buffers must pass through unchanged", d)
	}
	d, p, c = LevenshteinDistanceInto("", "", prev, curr)
	if d != 0 || &p[0] != &prev[0] || &c[0] != &curr[0] {
		t.Fatalf("both empty: got %d, buffers must pass through unchanged", d)
	}
}

func TestLevenshteinDistanceIntoNilBuffers(t *testing.T) {
	// nil work buffers behave exactly like the non-reusing API
	d, p, c := LevenshteinDistanceInto("kitten", "sitting", nil, nil)
	if d != LevenshteinDistance("kitten", "sitting") {
		t.Fatalf("got %d", d)
	}
	if p == nil || c == nil {
		t.Fatal("nil buffers must be grown for non-empty inputs")
	}
	if cap(p) < len("sitting")+1 || cap(c) < len("sitting")+1 {
		t.Fatalf("grown cap too small: %d/%d", cap(p), cap(c))
	}
}
