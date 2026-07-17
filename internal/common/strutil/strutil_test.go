// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package strutil

import (
	"testing"
)

func TestJoinStrings(t *testing.T) {
	tests := []struct {
		name string
		ss   []string
		sep  string
		want string
	}{
		{"normal join", []string{"a", "b", "c"}, ", ", "a, b, c"},
		{"empty slice", []string{}, ", ", ""},
		{"single element", []string{"only"}, " | ", "only"},
		{"containing empty strings", []string{"", "hello", ""}, "-", "-hello-"},
		{"empty separator", []string{"x", "y", "z"}, "", "xyz"},
		{"all empty", []string{"", "", ""}, ",", ",,"},
		{"nil slice", nil, ",", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := JoinStrings(tt.ss, tt.sep)
			if got != tt.want {
				t.Errorf("JoinStrings(%v, %q) = %q; want %q", tt.ss, tt.sep, got, tt.want)
			}
		})
	}
}

func TestSafeCharSlice(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxChars int
		want     string
	}{
		{"normal ASCII truncation", "hello world", 5, "hello"},
		{"shorter than max", "hi", 10, "hi"},
		{"exact boundary", "exact", 5, "exact"},
		{"empty string", "", 10, ""},
		{"maxChars is 0", "something", 0, ""},
		{"maxChars is negative", "something", -1, "something"},
		{"unicode characters", "你好世界", 2, "你好"},
		{"mixed ASCII and unicode", "a你b好c", 3, "a你b"},
		{"maxChars larger than length", "short", 100, "short"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeCharSlice(tt.input, tt.maxChars)
			if got != tt.want {
				t.Errorf("SafeCharSlice(%q, %d) = %q; want %q", tt.input, tt.maxChars, got, tt.want)
			}
		})
	}
}
