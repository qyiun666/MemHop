// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Capability path anchoring: memhop_capability_import is called by a model, so
// the files it may read are confined to the operator's --capability-dir. The
// same binary already anchors the database file to --db-dir with os.Root
// (registry.openShared); this is that pattern applied to the other path a
// client supplies.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveCapabilityPath turns a requested capability path into an absolute
// path inside capDir. Absolute requests are refused — the anchor is the
// operator's decision, not the caller's. os.Root rejects anything that resolves
// outside the directory, and the symlink comparison keeps the library's own read
// from being led out through a link placed inside it.
func resolveCapabilityPath(capDir, requested string) (string, error) {
	if requested == "" {
		return "", fmt.Errorf("capability path is required")
	}
	if filepath.IsAbs(requested) {
		return "", fmt.Errorf("capability path %q must be relative to the capability dir", requested)
	}
	root, err := os.OpenRoot(capDir)
	if err != nil {
		return "", fmt.Errorf("open capability dir: %w", err)
	}
	defer root.Close()

	rel := filepath.Clean(filepath.FromSlash(requested))
	if _, err := root.Stat(rel); err != nil {
		return "", fmt.Errorf("capability path %q is not reachable inside %s: %w", requested, capDir, err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(capDir, rel))
	if err != nil {
		return "", fmt.Errorf("resolve capability path: %w", err)
	}
	anchor, err := filepath.EvalSymlinks(capDir)
	if err != nil {
		return "", fmt.Errorf("resolve capability dir: %w", err)
	}
	if resolved != anchor && !strings.HasPrefix(resolved, anchor+string(filepath.Separator)) {
		return "", fmt.Errorf("capability path %q escapes the capability dir", requested)
	}
	return resolved, nil
}
