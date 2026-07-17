// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// String utility functions.

package strutil

import "unicode/utf8"

// SafeCharSlice returns a prefix of s up to maxChars runes.
func SafeCharSlice(s string, maxChars int) string {
	if utf8.RuneCountInString(s) <= maxChars {
		return s
	}
	var count int
	for i := range s {
		if count == maxChars {
			return s[:i]
		}
		count++
	}
	return s
}

// JoinStrings joins a string slice with the given separator.
func JoinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}
