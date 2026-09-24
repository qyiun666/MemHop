// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// OpenDB's rules: what the file holds and what the caller brought decide whether
// a database opens at all, and a refused open leaves nothing behind. The path's
// three states get their own test because one of them truncates.

package internal

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/internal/repo/core"
)

func testLLMConfig() LlmConfig {
	return LlmConfig{APIURL: "http://127.0.0.1:1/v1", APIKey: "test", Model: "mock"}
}

func primaryProfile(name string) *core.ProfileSlot {
	return &core.ProfileSlot{Name: name, Role: "assistant"}
}

// The branch that creates is the branch that truncates, so it may be reached
// only on a path confirmed absent. openEngine is called directly because the
// case that matters — a stat failure that is not "absent", on a path sitting
// next to real data — cannot be produced through a public entry point portably.
func TestOpenEngineCreatesOnlyOnAConfirmedAbsence(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	// A path under a regular file: stat fails, but not with "absent".
	if _, err := openEngine(filepath.Join(blocker, "db.meh"), true); common.CodeOf(err) != common.ErrIO {
		t.Fatalf("a path under a regular file: want ErrIO, got %v", err)
	}
	if body, err := os.ReadFile(blocker); err != nil || string(body) != "payload" {
		t.Fatalf("the neighbouring data must survive a refused open: %q %v", body, err)
	}

	// A directory is named as one, rather than reaching core.Open and coming
	// back as a file too small to hold the dual headers.
	if _, err := openEngine(dir, true); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("a directory as the database path: want ErrInvalidQuery, got %v", err)
	}

	// Absent and allowed: created.
	fresh := filepath.Join(dir, "fresh.meh")
	eng, err := openEngine(fresh, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Absent and not allowed: refused, with nothing left behind.
	another := filepath.Join(dir, "another.meh")
	if _, err := openEngine(another, false); common.CodeOf(err) != common.ErrConfig {
		t.Fatalf("an absent path with nothing to seed it: want ErrConfig, got %v", err)
	}
	if _, err := os.Stat(another); !os.IsNotExist(err) {
		t.Fatalf("a refused create left a file behind: %v", err)
	}
}

// No file and no profile is a refusal that must not leave a file: an empty one
// would make the next attempt take a different branch of the rules and report a
// different error for the same mistake.
func TestOpenDBRefusesToCreateWithoutAProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.meh")
	if _, err := OpenDB(path, testLLMConfig(), DefaultMemHopDefaults, nil); err == nil {
		t.Fatal("a new database with no primary profile must be refused")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the refused open left a file behind: %v", err)
	}
}

// No file, profile brought: created and seeded. The identity is stamped here, so
// a caller cannot open a file and claim its primary is a sub-agent.
func TestOpenDBCreatesAndSeedsThePrimary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.meh")
	slot := primaryProfile("Meow")
	slot.AgentType = core.AgentTypeSub
	db, err := OpenDB(path, testLLMConfig(), DefaultMemHopDefaults, slot)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	got, err := db.GetL0(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("GetL0: %v", err)
	}
	if got.Name != "Meow" || got.AgentType != core.AgentTypePrimary {
		t.Fatalf("seeded profile = %+v, want Meow stamped as the primary", got)
	}
	if got.UpdatedAtMs == 0 {
		t.Fatal("the library stamps UpdatedAtMs itself")
	}
	sess, err := db.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	read, err := sess.GetL0()
	if err != nil || read.Name != "Meow" {
		t.Fatalf("the primary session must reach the seeded profile: %+v %v", read, err)
	}
}

// A file whose primary is settled keeps its own profile: the argument is not
// consulted, so opening a file never rewrites whose memory it holds.
func TestOpenDBKeepsTheStoredPrimary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seeded.meh")
	first, err := OpenDB(path, testLLMConfig(), DefaultMemHopDefaults, primaryProfile("Original"))
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := OpenDB(path, testLLMConfig(), DefaultMemHopDefaults, primaryProfile("Usurper"))
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer second.Close()

	got, err := second.GetL0(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("GetL0: %v", err)
	}
	if got.Name != "Original" {
		t.Fatalf("the stored primary was overwritten with %q", got.Name)
	}
}

// A file that exists but holds no primary profile — one created by a path that
// never settled it — needs the argument, and is refused without one.
func TestOpenDBSeedsAnExistingFileWithNoPrimary(t *testing.T) {
	// A file that exists and holds nothing: open the engine and close it again
	// without settling a primary, which is the state a path that never seeded one
	// leaves behind.
	path := filepath.Join(t.TempDir(), "bare.meh")
	bare, err := openEngine(path, true)
	if err != nil {
		t.Fatalf("create the bare file: %v", err)
	}
	if err := bare.Close(); err != nil {
		t.Fatalf("close the bare file: %v", err)
	}

	if _, err := OpenDB(path, testLLMConfig(), DefaultMemHopDefaults, nil); err == nil {
		t.Fatal("an existing file with no primary profile must be refused without one")
	}
	// The refusal must not have disturbed the file: it still opens, and still
	// has no profile.
	db, err := OpenDB(path, testLLMConfig(), DefaultMemHopDefaults, primaryProfile("Late"))
	if err != nil {
		t.Fatalf("OpenDB with a profile: %v", err)
	}
	defer db.Close()
	got, err := db.GetL0(core.DefaultAgentID)
	if err != nil {
		t.Fatalf("GetL0: %v", err)
	}
	if got.Name != "Late" || got.AgentType != core.AgentTypePrimary {
		t.Fatalf("seeded profile = %+v", got)
	}
}

// A profile with no name cannot identify a domain, and a half-specified endpoint
// is refused before anything touches the filesystem.
func TestOpenDBRefusesIncompleteArguments(t *testing.T) {
	dir := t.TempDir()
	if _, err := OpenDB("", testLLMConfig(), DefaultMemHopDefaults, primaryProfile("x")); common.CodeOf(err) != common.ErrConfig {
		t.Fatalf("an empty path: want ErrConfig, got %v", err)
	}
	if _, err := OpenDB(filepath.Join(dir, "a.meh"), LlmConfig{APIURL: "http://x"}, DefaultMemHopDefaults, nil); common.CodeOf(err) != common.ErrConfig {
		t.Fatalf("a half-specified endpoint: want ErrConfig, got %v", err)
	}
	if _, err := OpenDB(filepath.Join(dir, "b.meh"), testLLMConfig(), DefaultMemHopDefaults, primaryProfile("   ")); common.CodeOf(err) != common.ErrInvalidQuery {
		t.Fatalf("a blank profile name: want ErrInvalidQuery, got %v", err)
	}
	// A retention window the sweep cannot represent is refused here rather than
	// redrawn: negative asks for a sweep that never runs, and a window past the
	// ceiling wraps the duration around into a cutoff in the future, which sweeps
	// everything the domain holds.
	for _, ms := range []int64{-1, 1 << 62, math.MaxInt64} {
		d := DefaultMemHopDefaults
		d.ContentRetentionMs = ms
		name := filepath.Join(dir, fmt.Sprintf("retention-%d.meh", ms))
		if _, err := OpenDB(name, testLLMConfig(), d, primaryProfile("x")); common.CodeOf(err) != common.ErrConfig {
			t.Fatalf("content_retention_ms %d: want ErrConfig, got %v", ms, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused open left %d files behind", len(entries))
	}
}

// An all-zero defaults table is what a host writes when it copies the shortest example,
// so the entry point answers it exactly as it answers DefaultMemHopDefaults: nothing is
// silently switched off, and nothing asks the model to compress a scene toward zero.
func TestOpenTakesUnfilledDefaultsAsTheLibraryDefaults(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "zero.meh"), testLLMConfig(), MemHopDefaults{}, primaryProfile("primary"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if db.config.Defaults != DefaultMemHopDefaults {
		t.Fatalf("the zero table reached the engine as %+v, want %+v", db.config.Defaults, DefaultMemHopDefaults)
	}
}
