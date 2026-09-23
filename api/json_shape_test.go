// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The other half of the promise: what a host stores, forwards, or shows a model is usually
// the encoded form, not the Go value — so "a host-visible list is always [] and a map always
// {}" has to hold after json.Marshal too, where an unset collection would otherwise arrive as
// `null`: a third answer nobody asked for, which a host re-decoding into its own type has to
// branch on. Field names come from this package's own structs, and a name counts only where
// every type declaring it declares it as a slice or map — so an intentionally absent scalar
// (parent_id, the anchor) is not swept in.
func TestEncodedCollectionsAreNeverNull(t *testing.T) {
	lists := listFieldNames(t)
	sess := openSurfaceDB(t)

	check := func(label string, v any, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		raw, merr := json.Marshal(v)
		if merr != nil {
			t.Fatalf("marshal %s: %v", label, merr)
		}
		var decoded any
		if uerr := json.Unmarshal(raw, &decoded); uerr != nil {
			t.Fatalf("unmarshal %s: %v", label, uerr)
		}
		for _, path := range nullLists(decoded, lists, label) {
			t.Errorf("%s encodes a collection as null at %s", label, path)
		}
	}

	// Before any round exists: the case where an unset collection is most likely to leak.
	prof, err := sess.GetL0()
	check("GetL0", prof, err)
	scenes, err := sess.ListScenes("")
	check("ListScenes", scenes, err)
	unread, err := sess.SceneContext("")
	check("SceneContext/unread", unread, err)

	if _, err := sess.Search(SearchQuery{}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if _, err := sess.AppendArchive(ArchiveInput{Kind: KindEvent, ContentType: ContentText,
		EventType: "tool_call", CreatedAt: turnStamp, Content: "did a thing"}); err != nil {
		t.Fatalf("AppendArchive: %v", err)
	}
	if _, err := sess.PlanNodeAdd(0, "step one"); err != nil {
		t.Fatalf("PlanNodeAdd: %v", err)
	}
	tree, err := sess.PlanState()
	check("PlanState", tree, err)
	topic, err := sess.Update(TurnEnd{Input: "in", Output: "out",
		Outcome: "answered", CreatedAt: turnStamp})
	check("Update", topic, err)
	sc, err := sess.SceneContext("")
	check("SceneContext", sc, err)
	arch, err := sess.SearchL4(L4Query{})
	check("SearchL4", arch, err)
	graphs, err := sess.ListL3()
	check("ListL3", graphs, err)
	l1, err := sess.ListL1()
	check("ListL1", l1, err)
	sr, err := sess.Search(SearchQuery{})
	check("Search", sr, err)
}

// The detector, pointed at a value that does carry nulls: a gate that cannot name the
// offender is decoration.
func TestNullListsDetectsTheLeak(t *testing.T) {
	type leaky struct {
		Topics   []string          `json:"topics"`
		Mapping  map[string]string `json:"mapping"`
		ParentID *string           `json:"parent_id"`
	}
	raw, err := json.Marshal(leaky{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	bad := nullLists(decoded, map[string]bool{"topics": true, "mapping": true}, "root")
	if len(bad) != 2 {
		t.Fatalf("detected %v, want the two collections reported and the nullable scalar left alone", bad)
	}
	if strings.Contains(strings.Join(bad, ","), "parent_id") {
		t.Fatalf("an intentionally absent scalar was flagged: %v", bad)
	}
}

// listFieldNames parses this package's structs and returns the json names that every
// declaring type uses for a slice or a map.
func listFieldNames(tb testing.TB) map[string]bool {
	tb.Helper()
	listSeen := map[string]bool{}
	scalarSeen := map[string]bool{}
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
		ast.Inspect(file, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok {
				return true
			}
			for _, fld := range st.Fields.List {
				jsonName, ok := jsonFieldName(fld)
				if !ok {
					continue
				}
				if isListType(fld.Type) {
					listSeen[jsonName] = true
					continue
				}
				scalarSeen[jsonName] = true
			}
			return true
		})
	}
	out := map[string]bool{}
	for name := range listSeen {
		if !scalarSeen[name] {
			out[name] = true
		}
	}
	if len(out) < 5 {
		tb.Fatalf("only %d collection-only field names found across the package", len(out))
	}
	return out
}

// jsonFieldName resolves the encoded key: the json tag when present, otherwise the lowercased
// Go name, which is what encoding/json itself does for an untagged exported field.
func jsonFieldName(fld *ast.Field) (string, bool) {
	if fld.Names == nil {
		return "", false
	}
	if fld.Tag != nil {
		tag := strings.Trim(fld.Tag.Value, "`\"")
		for _, part := range strings.Split(tag, ",") {
			if !strings.HasPrefix(part, "json:") {
				continue
			}
			name := strings.Trim(strings.TrimPrefix(part, "json:"), "\"")
			if name == "" {
				name = strings.ToLower(fld.Names[0].Name)
			}
			if name == "-" {
				return "", false
			}
			return name, true
		}
	}
	return strings.ToLower(fld.Names[0].Name), true
}

func isListType(e ast.Expr) bool {
	switch e.(type) {
	case *ast.ArrayType, *ast.MapType:
		return true
	}
	return false
}

// nullLists walks a decoded JSON tree and reports every path whose key names a collection
// field and whose value came out null.
func nullLists(v any, lists map[string]bool, path string) []string {
	var bad []string
	switch node := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(node))
		for k := range node {
			keys = append(keys, k)
		}
		for _, k := range keys {
			at := path + "." + k
			if node[k] == nil && lists[k] {
				bad = append(bad, at)
			}
			bad = append(bad, nullLists(node[k], lists, at)...)
		}
	case []any:
		for i, val := range node {
			bad = append(bad, nullLists(val, lists, path+"["+strconv.Itoa(i)+"]")...)
		}
	}
	return bad
}
