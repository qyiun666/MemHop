// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Both guides name package symbols and handle methods in prose and in the per-layer tables,
// not only in the §11 skeleton — and a host copies those lines too. The skeleton build proves
// one snippet compiles; this proves every `api.X` and every `handle.Method()` written anywhere
// in the guides exists on the published surface, so a rename or a retirement cannot leave a
// line behind that a host cannot compile.
//
// The symbol set is read from this package's own source rather than from a list kept here, so
// the check cannot drift from the thing it checks.
func TestGuidesReferenceOnlyPublishedSymbols(t *testing.T) {
	pkgs, methods := publishedSurface(t)

	for _, guide := range []string{"../INTEGRATION_GUIDE.md", "../INTEGRATION_GUIDE.zh.md"} {
		raw, err := os.ReadFile(guide)
		if err != nil {
			t.Fatalf("read guide %s: %v", guide, err)
		}
		apiNames, methodNames := referencedNames(string(raw))
		if bad := unresolved(apiNames, methodNames, pkgs, methods); len(bad) > 0 {
			t.Fatalf("%s names symbols the package does not publish: %s", guide, strings.Join(bad, ", "))
		}
		if len(apiNames) == 0 || len(methodNames) == 0 {
			t.Fatalf("%s yielded no symbols to check (%d api names, %d methods) — the pattern stopped matching",
				guide, len(apiNames), len(methodNames))
		}
		t.Logf("%s: %d package symbols, %d method calls, all published", guide, len(apiNames), len(methodNames))
	}
}

// The same check, pointed at a text carrying one invented name and two retired ones: a gate
// that cannot say which symbol is missing is decoration.
func TestUnresolvedFlagsInventedAndRetiredNames(t *testing.T) {
	pkgs, methods := publishedSurface(t)
	apiNames, methodNames := referencedNames("use api.SessionContextTopic2, call db.PlanCreate(), " +
		"db.ListCapabilities(), sess.BogusMethod() — but api.LlmConfig and db.Search are real")
	bad := unresolved(apiNames, methodNames, pkgs, methods)
	sort.Strings(bad)
	want := []string{"BogusMethod", "ListCapabilities", "PlanCreate", "SessionContextTopic2"}
	if len(bad) != len(want) {
		t.Fatalf("unresolved %v, want %v", bad, want)
	}
	for i := range want {
		if bad[i] != want[i] {
			t.Fatalf("unresolved %v, want %v", bad, want)
		}
	}
}

// publishedSurface parses this package's non-test sources once.
func publishedSurface(tb testing.TB) (pkgs map[string]bool, methods map[string]bool) {
	tb.Helper()
	pkgs = map[string]bool{}
	methods = map[string]bool{}
	fset := token.NewFileSet()
	files, err := os.ReadDir(".")
	if err != nil {
		tb.Fatalf("read package dir: %v", err)
	}
	for _, f := range files {
		name := f.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			tb.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() {
					continue
				}
				if d.Recv != nil {
					methods[d.Name.Name] = true
					continue
				}
				pkgs[d.Name.Name] = true
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							pkgs[s.Name.Name] = true
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								pkgs[n.Name] = true
							}
						}
					}
				}
			}
		}
	}
	if len(pkgs) < 20 || len(methods) < 20 {
		tb.Fatalf("the parsed surface looks wrong: %d package symbols, %d methods", len(pkgs), len(methods))
	}
	return pkgs, methods
}

var (
	apiRef    = regexp.MustCompile(`api\.([A-Z][A-Za-z0-9_]*)`)
	handleRef = regexp.MustCompile(`\b(?:db|sess|session|worker|lib|m|handle)\.([A-Z][A-Za-z0-9_]*)\(`)
)

func referencedNames(text string) (apiNames, methodNames []string) {
	seenAPI := map[string]bool{}
	for _, m := range apiRef.FindAllStringSubmatch(text, -1) {
		if !seenAPI[m[1]] {
			seenAPI[m[1]] = true
			apiNames = append(apiNames, m[1])
		}
	}
	seenMeth := map[string]bool{}
	for _, m := range handleRef.FindAllStringSubmatch(text, -1) {
		if !seenMeth[m[1]] {
			seenMeth[m[1]] = true
			methodNames = append(methodNames, m[1])
		}
	}
	return apiNames, methodNames
}

// unresolved reports every referenced name that is neither a published package symbol nor a
// method on a published type. A handle method may also be a package name (a type used through
// an embedded value), so both sets answer.
func unresolved(apiNames, methodNames []string, pkgs, methods map[string]bool) []string {
	var bad []string
	for _, n := range apiNames {
		if !pkgs[n] {
			bad = append(bad, n)
		}
	}
	for _, n := range methodNames {
		if !methods[n] && !pkgs[n] {
			bad = append(bad, n)
		}
	}
	sort.Strings(bad)
	return bad
}

// The same pairing in the other direction: a type the package publishes but neither guide
// names is a capability a host cannot discover from its entry document — it will not guess
// that `DB.Stats()` exists, and a surface that must be read out of `go doc` is not
// "integrate and use". A type absent from both guides fails this; add a row to §9, do not
// delete the type to make it pass.
func TestEveryPublishedTypeIsDocumented(t *testing.T) {
	types := publishedTypes(t)
	for _, guide := range []string{"../INTEGRATION_GUIDE.md", "../INTEGRATION_GUIDE.zh.md"} {
		raw, err := os.ReadFile(guide)
		if err != nil {
			t.Fatalf("read guide %s: %v", guide, err)
		}
		if missing := undocumentedTypes(string(raw), types); len(missing) > 0 {
			t.Fatalf("%s never mentions %v — the published types a host cannot discover from it",
				guide, missing)
		}
	}
}

// The detector, pointed at a text that leaves two published types out: a check that cannot
// name the missing one is a list comparison, not a gate.
func TestUndocumentedTypesNamesTheGap(t *testing.T) {
	text := "mentions ProfileSlot and TopicSlot and nothing else"
	missing := undocumentedTypes(text, map[string]bool{
		"ProfileSlot": true, "TopicSlot": true, "ArchiveSlot": true, "L3ImportItem": true,
	})
	if len(missing) != 2 || missing[0] != "ArchiveSlot" || missing[1] != "L3ImportItem" {
		t.Fatalf("missing %v, want [ArchiveSlot L3ImportItem]", missing)
	}
}

func undocumentedTypes(text string, types map[string]bool) []string {
	var missing []string
	for name := range types {
		if !strings.Contains(text, name) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// publishedTypes parses this package's files for exported type declarations.
func publishedTypes(tb testing.TB) map[string]bool {
	tb.Helper()
	out := map[string]bool{}
	fset := token.NewFileSet()
	files, err := os.ReadDir(".")
	if err != nil {
		tb.Fatalf("read package dir: %v", err)
	}
	for _, f := range files {
		name := f.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			tb.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() {
					out[ts.Name.Name] = true
				}
			}
		}
	}
	if len(out) < 20 {
		tb.Fatalf("only %d exported types parsed — the walk stopped working", len(out))
	}
	return out
}
