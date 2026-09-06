// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

var builtinNames = []string{
	"memhop-guide", "memhop-cycle", "memhop-profile", "memhop-scene",
	"memhop-knowledge", "memhop-archive", "memhop-capability",
	"memhop-trajectory", "memhop-plan",
}

// The assembled manuals must be valid active capabilities with stable
// name-derived IDs, one card per manual, each carrying at least one function
// entry.
func TestBuiltinCards(t *testing.T) {
	caps := BuiltinCards()
	want := len(builtinNames)
	if len(caps) != want {
		t.Fatalf("want %d builtin capabilities, got %d", want, len(caps))
	}
	seen := map[string]struct{}{}
	for _, c := range caps {
		seen[c.Name] = struct{}{}
		if c.Status != core.CapabilityActive || c.Origin != core.CapabilityOriginBuiltin {
			t.Fatalf("builtin %s: status=%v origin=%v", c.Name, c.Status, c.Origin)
		}
		if c.IDHash != core.CapabilityID(c.Name) {
			t.Fatalf("builtin %s: unstable ID %d", c.Name, c.IDHash)
		}
		if c.Package != c.Name {
			t.Fatalf("builtin %s: package stamp %q", c.Name, c.Package)
		}
		if c.Summary == "" || c.Trigger == "" {
			t.Fatalf("builtin %s: missing summary or trigger", c.Name)
		}
		if len(c.Resources) == 0 {
			t.Fatalf("builtin %s: no resource entries", c.Name)
		}
		for _, r := range c.Resources {
			if r.Type != core.CapabilityAPI || r.Ref == "" || r.Desc == "" {
				t.Fatalf("builtin %s: malformed resource %+v", c.Name, r)
			}
		}
	}
	for _, name := range builtinNames {
		if _, ok := seen[name]; !ok {
			t.Fatalf("builtin %s missing", name)
		}
	}
}
