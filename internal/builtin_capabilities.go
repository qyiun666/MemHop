// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Built-in capability loading for the composition root. The api facade
// injects the embedded memhop-capability/v3 toolbox (root capabilities/
// directory) as an fs.FS — internal never imports the capabilities package
// (dependency discipline: the data leaf is provided by the assembly
// entry point). Attaching the manuals to the L5 read APIs is assembly work
// owned by internal.Open, not by the public facade.

package internal

import (
	"io/fs"
	"path"
	"strings"

	"github.com/qyiun666/MemHop/internal/cap/capability"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// loadBuiltinCapabilities parses every *.json manual in the injected
// toolbox into read-only in-memory capabilities with stable name-derived
// IDs. The files are validated by unit tests, so a parse failure here means
// a corrupted build and aborts Open.
func loadBuiltinCapabilities(builtins fs.FS) ([]core.Capability, error) {
	entries, err := fs.ReadDir(builtins, ".")
	if err != nil {
		return nil, err
	}
	out := make([]core.Capability, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := fs.ReadFile(builtins, path.Clean(entry.Name()))
		if err != nil {
			return nil, err
		}
		cap, err := capability.Build(data, "builtin:"+entry.Name())
		if err != nil {
			return nil, err
		}
		cap.IDHash = core.CapabilityID(cap.Name)
		cap.Status = core.CapabilityActive
		cap.Origin = core.CapabilityOriginBuiltin
		out = append(out, *cap)
	}
	return out, nil
}
