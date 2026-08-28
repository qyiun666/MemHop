// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

import (
	"testing"

	"github.com/qyiun666/MemHop/capabilities"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

var builtinNames = []string{
	"memhop-guide", "memhop-knowledge", "memhop-scene", "memhop-archive",
	"memhop-profile", "memhop-capability",
}

// The embedded toolbox must parse into valid active capabilities with
// stable name-derived IDs; a corrupted embedded file fails here.
func TestLoadBuiltinCapabilities(t *testing.T) {
	caps, err := loadBuiltinCapabilities(capabilities.FS)
	if err != nil {
		t.Fatalf("loadBuiltinCapabilities: %v", err)
	}
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
		if c.FileHash == "" {
			t.Fatalf("builtin %s: missing file hash", c.Name)
		}
		switch c.Type {
		case core.CapabilityMCP, core.CapabilitySkill, core.CapabilityAPI:
			if len(c.Resources) != 1 || c.Resources[0].Type != c.Type {
				t.Fatalf("builtin %s: resources mismatch %+v", c.Name, c)
			}
		case core.CapabilityComposite:
			if len(c.Resources) == 0 {
				t.Fatalf("builtin composite %s: no resources %+v", c.Name, c)
			}
		default:
			t.Fatalf("builtin %s: unexpected type %v", c.Name, c.Type)
		}
	}
	for _, name := range builtinNames {
		if _, ok := seen[name]; !ok {
			t.Fatalf("builtin %s missing", name)
		}
	}
}
