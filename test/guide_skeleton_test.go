// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// `make check-guides` compiles the runnable skeleton embedded in both integration guides,
// which proves the API names fit together - and nothing else. A quickstart that builds and
// then dies on its first call is precisely the failure a host meets when it follows the
// document instead of the tests, so this takes the same extracted program, builds it, and
// runs it end to end against the package's own stub endpoint.
//
// It is a separate check from the skeleton build on purpose: running needs the module wiring
// to resolve a replaced dependency, and the program must be a guest in a temporary module the
// way a real host's would be.
func TestGuideSkeletonActuallyRuns(t *testing.T) {
	repo := repoRoot(t)
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go toolchain on PATH: %v", err)
	}

	for _, guide := range []struct{ name, skel string }{
		{"INTEGRATION_GUIDE.md", "skeleton-en"},
		{"INTEGRATION_GUIDE.zh.md", "skeleton-zh"},
	} {
		t.Run(guide.name, func(t *testing.T) {
			src := extractSkeleton(t, filepath.Join(repo, guide.name))
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(
				"module memhop.guidecheck\n\ngo 1.27\n\nrequire github.com/qyiun666/MemHop v0.0.0\n\n"+
					"replace github.com/qyiun666/MemHop => "+repo+"\n"), 0o644); err != nil {
				t.Fatalf("write go.mod: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
				t.Fatalf("write main.go: %v", err)
			}

			// `go build` appends .exe where the platform needs one, so exec'ing the name we
			// asked for would look for a file that was never written: on Windows the binary is
			// <name>.exe and the plain name answers "executable file not found in %PATH%".
			name := guide.skel
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			bin := filepath.Join(dir, name)
			build := exec.Command(goBin, "build", "-o", bin, ".")
			build.Dir = dir
			build.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build the extracted skeleton: %v\n%s", err, out)
			}

			stub := newMockLLM(t)
			run := exec.Command(bin)
			run.Dir = dir
			run.Env = append(os.Environ(),
				"MEH_PATH="+filepath.Join(dir, "guide.meh"),
				"LLM_URL="+stub.srv.URL,
				"LLM_KEY=stub",
				"LLM_MODEL=stub-model",
			)
			out, runErr := run.CombinedOutput()
			t.Logf("skeleton output:\n%s", strings.TrimSpace(string(out)))
			if runErr != nil {
				t.Fatalf("the documented quickstart did not run: %v\noutput: %s", runErr, out)
			}
			if strings.Contains(string(out), "panic:") {
				t.Fatalf("the documented quickstart panicked:\n%s", out)
			}
			// The run's exit code only says nothing crashed. These are the parts of the
			// loop the skeleton promises a host will have in hand before the next model
			// call — the profile block, and the plan read back as a folded forest with
			// its rollups — so the copy-paste example is checked by what it prints, not
			// by its return code.
			for _, want := range []string{"name:", "plan: 1/2 steps done", "- #1 ", "[done]"} {
				if !strings.Contains(string(out), want) {
					t.Errorf("the skeleton printed no %q; the documented loop is missing a step it advertises\noutput:\n%s",
						want, strings.TrimSpace(string(out)))
				}
			}
		})
	}
}

// repoRoot is the checkout both guides live in.
func repoRoot(tb testing.TB) string {
	tb.Helper()
	repo, err := filepath.Abs("..")
	if err != nil {
		tb.Fatalf("resolve repo root: %v", err)
	}
	return repo
}

// A checkout on Windows carries CRLF, so the extraction that finds the quickstart has to read
// the document rather than a particular line ending. This is checked on every platform on
// purpose: the failure it pins showed up as "the guide has no skeleton" on a runner where the
// guide was intact, and no other test in this file would notice the extractor going blind.
func TestSkeletonExtractionToleratesCRLF(t *testing.T) {
	repo := repoRoot(t)
	for _, name := range []string{"INTEGRATION_GUIDE.md", "INTEGRATION_GUIDE.zh.md"} {
		path := filepath.Join(repo, name)
		windows := filepath.Join(t.TempDir(), name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		// Normalise first: a checkout that already carries CRLF would otherwise get a
		// doubled carriage return here, and one TrimSuffix could not take it back off.
		crlf := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n", "\r\n")
		if err := os.WriteFile(windows, []byte(crlf), 0o644); err != nil {
			t.Fatalf("write CRLF copy: %v", err)
		}
		if got, want := extractSkeleton(t, windows), extractSkeleton(t, path); got != want {
			t.Errorf("%s: the CRLF checkout yielded a different skeleton than the LF one\n crlf: %q\n  lf: %q",
				name, firstLine(got), firstLine(want))
		}
	}
}

// firstLine keeps a mismatch report readable: the skeletons are whole programs.
func firstLine(src string) string {
	line, _, _ := strings.Cut(src, "\n")
	return line
}

// extractSkeleton returns the first fenced go block that opens with a package clause, the same
// rule `make check-guides` applies, so the two checks cannot drift apart in what they read.
// The line ends are dropped rather than compared: a Windows checkout carries CRLF, and a
// document that only differs by its record separator is the same document.
func extractSkeleton(tb testing.TB, path string) string {
	tb.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("read %s: %v", path, err)
	}
	var buf strings.Builder
	inBlock := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case line == "```go" && !inBlock:
			inBlock = true
			buf.Reset()
		case line == "```" && inBlock:
			inBlock = false
			if strings.HasPrefix(buf.String(), "package ") {
				return buf.String()
			}
			buf.Reset()
		case inBlock:
			buf.WriteString(line)
			buf.WriteString("\n")
		}
	}
	tb.Fatalf("%s has no package-main go block", path)
	return ""
}
