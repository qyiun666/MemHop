// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"strings"
	"testing"
)

// TestInitTokenizerEngineMismatch verifies the process-level singleton
// contract: re-initializing with a different engine must return an explicit
// error instead of silently keeping the first instance.
func TestInitTokenizerEngineMismatch(t *testing.T) {
	ResetTokenizer()
	t.Cleanup(ResetTokenizer)

	if err := InitTokenizer(EngineAuto); err != nil {
		t.Fatalf("InitTokenizer(auto): %v", err)
	}
	// Same engine again is a no-op ("" normalizes to auto).
	if err := InitTokenizer(""); err != nil {
		t.Fatalf("repeated InitTokenizer with same engine should be a no-op: %v", err)
	}
	// Different engine must be rejected.
	err := InitTokenizer(EngineGse)
	if err == nil {
		t.Fatal("InitTokenizer(gse) after auto should fail")
	}
	if !strings.Contains(err.Error(), "already initialized") {
		t.Errorf("error should mention prior initialization, got: %v", err)
	}
}

// TestInitTokenizerUnknownEngine verifies unsupported engine names are
// rejected instead of silently downgraded.
func TestInitTokenizerUnknownEngine(t *testing.T) {
	ResetTokenizer()
	t.Cleanup(ResetTokenizer)

	if err := InitTokenizer("jieba"); err == nil {
		t.Fatal("InitTokenizer with unknown engine should fail")
	}
}
