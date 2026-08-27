// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package common

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

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

func LevenshteinDistance(a, b string) int {
	d, _, _ := LevenshteinDistanceInto(a, b, nil, nil)
	return d
}

// LevenshteinDistanceInto computes the edit distance between a and b
// using caller-provided work buffers, growing them when capacity is
// insufficient. The returned prev/curr slices are the (possibly grown)
// buffers and may be passed back as arguments for zero-allocation reuse
// across calls; on the empty-string fast paths they are returned
// unchanged. The result is identical to LevenshteinDistance.
func LevenshteinDistanceInto(a, b string, prev, curr []int) (int, []int, []int) {
	aRunes := []rune(a)
	bRunes := []rune(b)
	m, n := len(aRunes), len(bRunes)
	if m == 0 {
		return n, prev, curr
	}
	if n == 0 {
		return m, prev, curr
	}
	need := n + 1
	if cap(prev) < need {
		prev = make([]int, need)
	} else {
		prev = prev[:need]
	}
	if cap(curr) < need {
		curr = make([]int, need)
	} else {
		curr = curr[:need]
	}
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
	return prev[n], prev, curr
}

// SplitCamelCase splits camelCase/PascalCase identifiers
// ("fetchUserData" → [fetch, user, data], "JSONParser" → [json, parser]);
// tokens containing '_' are kept intact.
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

func TrimPunctuation(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
}

// TruncateUTF8 cuts s to at most max bytes without splitting a UTF-8 rune.
func TruncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	t := s[:max]
	for len(t) > 0 && !utf8.ValidString(t) {
		t = t[:len(t)-1]
	}
	return t
}
