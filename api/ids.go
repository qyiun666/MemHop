// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// External id rendering of the public facade: pure forwarding to the
// internal seam (internal/exports.go); the hex format rule lives there.

package api

import "github.com/qyiun666/MemHop/internal"

// FormatAgentID renders an agentID as its external 16-char hex form.
func FormatAgentID(agentID uint64) string { return internal.FormatAgentID(agentID) }

// ParseAgentID parses a 16-char hex agentID.
func ParseAgentID(s string) (uint64, error) { return internal.ParseAgentID(s) }

// FormatID renders any record ID as its external 16-char hex form.
func FormatID(id uint64) string { return internal.FormatID(id) }

// ParseID parses a 16-char hex record ID.
func ParseID(s string) (uint64, error) { return internal.ParseID(s) }
