// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package capabilities ships MemHop's built-in L5 capability cards
// (memhop-capability/v3 JSON): usage manuals for the capabilities an LLM
// may call itself — L2 scenes, L3 knowledge, L4 archive, L0 profile, L6
// trajectory and the L5 lifecycle — plus a guide card that documents the
// loop split (Search / Update / Dream run host-side, never manual LLM
// calls) and indexes the other cards. The files are embedded so the Go
// library can load them without any on-disk path.
//
// Data only: this package must never import other MemHop packages.
package capabilities

import "embed"

// FS holds the built-in memhop-capability/v3 cards, one *.json file per
// capability. Every Open attaches them automatically to the L5 read APIs
// (ListCapabilities / GetCapability). They are read-only: lifecycle writes
// (Activate/Usage/Delete/Update) reject them.
//
//go:embed *.json
var FS embed.FS
