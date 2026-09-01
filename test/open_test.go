// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build integration

package test

import (
	"testing"

	"github.com/qyiun666/MemHop/test/testsupport"
)

// TestOpenSmoke verifies that a MemHop database can be opened against the real
// LLM endpoint and closed cleanly.
func TestOpenSmoke(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()

	if db.IsClosed() {
		t.Fatal("db should be open")
	}
}
