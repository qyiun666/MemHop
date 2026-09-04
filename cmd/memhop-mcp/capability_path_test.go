// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The capability path comes from a model, so every way of naming something
// outside the anchor has to be refused rather than read.
func TestResolveCapabilityPathStaysInsideAnchor(t *testing.T) {
	capDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(capDir, "card.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(capDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(capDir, "nested", "card.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(capDir, "link.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	cases := []struct {
		name      string
		requested string
		wantOK    bool
	}{
		{"plain file", "card.json", true},
		{"nested file", "nested/card.json", true},
		{"dot-prefixed relative", "./nested/../card.json", true},
		{"empty", "", false},
		{"absolute", outside, false},
		{"parent escape", "../secret.json", false},
		{"deep parent escape", "nested/../../secret.json", false},
		{"symlink out of the anchor", "link.json", false},
		{"missing file", "nope.json", false},
	}
	for _, tc := range cases {
		got, err := resolveCapabilityPath(capDir, tc.requested)
		switch {
		case tc.wantOK && err != nil:
			t.Fatalf("%s: want accepted, got %v", tc.name, err)
		case !tc.wantOK && err == nil:
			t.Fatalf("%s: want refused, got %q", tc.name, got)
		}
		if !tc.wantOK && !strings.Contains(err.Error(), tc.requested) && tc.requested != "" {
			t.Fatalf("%s: the refusal should name the rejected path, got %v", tc.name, err)
		}
	}
}
