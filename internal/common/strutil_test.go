// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package common

import (
	"testing"
	"unicode/utf8"
)

func TestTruncateUTF8(t *testing.T) {
	s := "你好世界"
	if got := TruncateUTF8(s, 4); got != "你" {
		t.Fatalf("truncate 4: %q", got)
	}
	if got := TruncateUTF8(s, 6); got != "你好" {
		t.Fatalf("truncate 6: %q", got)
	}
	if got := TruncateUTF8(s, 100); got != s {
		t.Fatalf("truncate 100: %q", got)
	}
	if got := TruncateUTF8("a"+s, 5); utf8.ValidString(got) == false {
		t.Fatalf("result invalid utf8: %q", got)
	}
}
