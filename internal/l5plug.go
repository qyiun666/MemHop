// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// plug/ auto-injection: every subdirectory of <meh dir>/plug holding a
// memhop-capability/v4 package (capability.json) is imported into the shared
// L5 pool at Open. A broken package is warned and skipped — a bad user plugin
// must not brick the database open — while good packages land. Re-opening is
// idempotent: a byte-identical re-import writes nothing (importCapabilities'
// FileHash guard), so the append-only file does not grow on every start.

package internal

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/qyiun666/MemHop/internal/cap/capability"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

// plugDirName is the fixed plugin-package directory next to the .meh file.
const plugDirName = "plug"

// injectPlugDir scans <dir(mehPath)>/plug/<package>/capability.json and
// imports every package into the shared L5 pool. A missing plug directory is
// a no-op; non-directory entries and folders without capability.json are
// ignored; a package that fails to parse or validate is warned and skipped.
// Called once from Open while no tenant is served yet: the pool lock is
// taken directly, without the caller-existence check a session op needs.
func (db *DB) injectPlugDir(mehPath string) {
	plugDir := filepath.Join(filepath.Dir(mehPath), plugDirName)
	entries, err := os.ReadDir(plugDir)
	if err != nil {
		// A missing plug directory is the normal case; any other read
		// failure (permissions, IO) would silently disable the whole
		// feature, so it is reported.
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("plug: scan failed", "dir", plugDir, "error", err)
		}
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pkgPath := filepath.Join(plugDir, e.Name())
		db.injectOnePackage(pkgPath)
	}
}

// injectOnePackage parses and imports one package directory, warning instead
// of failing on a broken one (Open must survive a bad plugin).
func (db *DB) injectOnePackage(pkgPath string) {
	data, _, err := capability.ReadFile(pkgPath)
	if err != nil {
		slog.Warn("plug: skip package", "path", pkgPath, "error", err)
		return
	}
	caps, err := capability.BuildPackage(data, pkgPath)
	if err != nil {
		slog.Warn("plug: skip package", "path", pkgPath, "error", err)
		return
	}
	ac, err := db.contextFor(core.SharedPoolAgentID)
	if err != nil {
		slog.Warn("plug: skip package", "path", pkgPath, "error", err)
		return
	}
	ac.Mu.Lock()
	defer ac.Mu.Unlock()
	result := db.importCapabilitiesLocked(caps)
	if len(result.CreatedIDs)+len(result.UpdatedIDs) > 0 {
		slog.Info("plug: package injected",
			"path", pkgPath, "created", len(result.CreatedIDs), "updated", len(result.UpdatedIDs))
	}
	for _, e := range result.Errors {
		slog.Warn("plug: card import failed", "path", pkgPath, "error", e)
	}
}
