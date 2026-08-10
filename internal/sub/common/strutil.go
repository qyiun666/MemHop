// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// String utility functions.

package common

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

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

// LevenshteinDistance computes the edit distance between two strings.
func LevenshteinDistance(a, b string) int {
	aRunes := []rune(a)
	bRunes := []rune(b)
	m, n := len(aRunes), len(bRunes)
	if m == 0 {
		return n
	}
	if n == 0 {
		return m
	}

	prev := make([]int, n+1)
	curr := make([]int, n+1)
	for j := 0; j <= n; j++ {
		prev[j] = j
	}
	for i := 1; i <= m; i++ {
		curr[0] = i
		for j := 1; j <= n; j++ {
			cost := 1
			if aRunes[i-1] == bRunes[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[n]
}

// SplitCamelCase splits camelCase/PascalCase identifiers:
//
//	"fetchUserData" → ["fetch", "user", "data"]
//	"JSONParser"    → ["json", "parser"]
//	"getUserID"     → ["get", "user", "id"]
//
// Tokens containing '_' are kept intact.
func SplitCamelCase(word string) []string {
	if strings.Contains(word, "_") {
		return []string{word}
	}

	runes := []rune(word)
	hasUpper, hasLower := false, false
	for _, r := range runes {
		if unicode.IsUpper(r) {
			hasUpper = true
		}
		if unicode.IsLower(r) {
			hasLower = true
		}
	}
	if !hasUpper || !hasLower {
		return []string{word}
	}

	lower := []rune(strings.ToLower(word))
	n := len(runes)
	var parts []string
	start := 0

	for i := 1; i < n; i++ {
		if unicode.IsLower(runes[i-1]) && unicode.IsUpper(runes[i]) {
			parts = append(parts, string(lower[start:i]))
			start = i
			continue
		}
		if i+1 < n && unicode.IsUpper(runes[i-1]) && unicode.IsUpper(runes[i]) && unicode.IsLower(runes[i+1]) {
			parts = append(parts, string(lower[start:i]))
			start = i
		}
	}
	parts = append(parts, string(lower[start:]))
	return parts
}

// TrimPunctuation removes non-alphanumeric (except '_') from both ends.
func TrimPunctuation(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
}
