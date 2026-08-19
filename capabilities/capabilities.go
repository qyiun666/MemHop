// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Package capabilities ships MemHop's built-in L5 capability cards
// (memhop-capability/v2 JSON): wrappers that teach a host agent how to
// drive the memory loop — Search / Update / Dream / L7 trajectory / L5
// crystallize and import. The files are embedded so the Go library can
// load them without any on-disk path.
//
// Data only: this package must never import other MemHop packages.
package capabilities

import "embed"

// FS holds the built-in memhop-capability/v2 cards, one *.json file per
// capability. Every Open attaches them automatically to the L5 read APIs
// (ListCapabilities / GetCapability). They are read-only: lifecycle writes
// (Activate/Usage/Delete/Update) reject them.
//
//go:embed *.json
var FS embed.FS
