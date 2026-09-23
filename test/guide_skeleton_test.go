// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package test

import (
	"os"
	"os/exec"
	"path/filepath"
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
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
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

			bin := filepath.Join(dir, guide.skel)
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
		})
	}
}

// extractSkeleton returns the first fenced go block that opens with a package clause, the same
// rule `make check-guides` applies, so the two checks cannot drift apart in what they read.
func extractSkeleton(tb testing.TB, path string) string {
	tb.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("read %s: %v", path, err)
	}
	var buf strings.Builder
	inBlock := false
	for _, line := range strings.Split(string(raw), "\n") {
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
