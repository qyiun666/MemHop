// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// `internal` is not published, so `go doc github.com/qyiun666/MemHop/api.Session` is the only
// documentation a host can read for the methods this facade declares - a method without a
// comment line is a method no host can learn from short of reading the source, which is the
// opposite of "integrate and use". This walks the same files the guide-symbol gate parses and
// requires every exported method of the two handles to be documented, with the count pinned so
// a broken walk cannot pass by finding nothing.
func TestEveryFacadeMethodIsDocumented(t *testing.T) {
	fset := token.NewFileSet()
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	checked := 0
	for _, f := range files {
		name := f.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || !fd.Name.IsExported() {
				continue
			}
			switch receiverName(fd.Recv.List[0].Type) {
			case "Session", "DB":
			default:
				continue
			}
			checked++
			if strings.TrimSpace(fd.Doc.Text()) == "" {
				t.Errorf("%s: (%s).%s is exported but carries no doc comment - invisible to `go doc`, which is a host's only entry for it",
					name, receiverName(fd.Recv.List[0].Type), fd.Name.Name)
			}
		}
	}
	if checked != 34 {
		t.Fatalf("documented-method walk covered %d methods, want the 34 the facade publishes (26 Session + 8 DB)", checked)
	}
}

func receiverName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.Ident:
		return t.Name
	}
	return ""
}
