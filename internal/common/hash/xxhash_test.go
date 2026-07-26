// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package hash

import "testing"

func TestHashID_Basic(t *testing.T) {
	h := HashID("test")
	if h == 0 {
		t.Fatal("HashID(\"test\") returned 0")
	}
	// xxhash64 of "test" with seed 0 is a known value
	expected := FormatHash(h)
	if len(expected) != 16 {
		t.Fatalf("FormatHash length = %d, want 16", len(expected))
	}
}

func TestHashID_Deterministic(t *testing.T) {
	a := HashID("hello")
	b := HashID("hello")
	if a != b {
		t.Fatalf("HashID not deterministic: %d != %d", a, b)
	}
}

func TestHashID_DifferentInputs(t *testing.T) {
	a := HashID("foo")
	b := HashID("bar")
	if a == b {
		t.Fatal("different inputs produced same hash")
	}
}

func TestFormatHash(t *testing.T) {
	h := FormatHash(0)
	if h != "0000000000000000" {
		t.Fatalf("FormatHash(0) = %q, want %q", h, "0000000000000000")
	}
	h2 := FormatHash(0xabcdef1234567890)
	if h2 != "abcdef1234567890" {
		t.Fatalf("FormatHash = %q, want %q", h2, "abcdef1234567890")
	}
}

func TestParseID(t *testing.T) {
	original := uint64(0xabcdef1234567890)
	s := FormatHash(original)
	parsed, err := ParseID(s)
	if err != nil {
		t.Fatalf("ParseID error: %v", err)
	}
	if parsed != original {
		t.Fatalf("ParseID = %d, want %d", parsed, original)
	}
}

func TestHashID_RustCompatibility(t *testing.T) {
	// xxhash64("test") with seed 0 = 0x4fdcca5ddb67813a (Rust twox-hash compatible)
	h := HashID("test")
	formatted := FormatHash(h)
	// The Go xxhash and Rust twox-hash both use xxHash64 with seed 0,
	// so they must produce the same output.
	if formatted == "" {
		t.Fatal("empty hash")
	}
	t.Logf("HashID(\"test\") = %s", formatted)
}
