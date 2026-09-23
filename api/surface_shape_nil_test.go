// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

// The stated contract is that a host-visible list is always [] and a map always {}: an
// absent collection is a fact the host can render, while a nil one is a branch it has to
// remember to write, and `null` in JSON is a third answer nobody asked for. This walks every
// read on the session surface twice — before anything has been written, and after a round, a
// plan step and a graph exist — because the empty case is where a nil leaks through.
func TestSurfaceReadsNeverReturnNilCollections(t *testing.T) {
	sess := openSurfaceDB(t)

	check := func(label string, v any, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		var found []string
		walkNils(reflect.ValueOf(v), label, &found)
		for _, f := range found {
			t.Errorf("nil collection crosses the facade at %s", f)
		}
	}
	reads := func(withGraph bool) {
		t.Helper()
		prof, err := sess.GetL0()
		check("GetL0", prof, err)
		nodes, err := sess.ListL1()
		check("ListL1", nodes, err)
		scenes, err := sess.ListScenes("")
		check("ListScenes", scenes, err)
		graphs, err := sess.ListL3()
		check("ListL3", graphs, err)
		arch, err := sess.SearchL4(L4Query{})
		check("SearchL4", arch, err)
		sc, err := sess.SceneContext("")
		check("SceneContext", sc, err)
		sr, err := sess.Search(SearchQuery{})
		check("Search", sr, err)
		tree, err := sess.PlanState()
		check("PlanState", tree, err)
		if !withGraph {
			return
		}
		g, err := sess.GetL3(graphs[0].ID)
		check("GetL3", g, err)
		gnodes, err := sess.QueryL3Nodes(L3NodeQuery{GraphID: graphs[0].ID})
		check("QueryL3Nodes", gnodes, err)
		sub, err := sess.QueryL3Subgraph(graphs[0].ID, gnodes[0].ID, 3, nil)
		check("QueryL3Subgraph", sub, err)
		dream, err := sess.Dream(context.Background(), "")
		check("Dream", dream, err)
	}

	reads(false)

	if _, err := sess.AppendArchive(ArchiveInput{Kind: KindEvent, ContentType: ContentText,
		EventType: "tool_call", CreatedAt: turnStamp, Content: "did a thing"}); err != nil {
		t.Fatalf("AppendArchive: %v", err)
	}
	if _, err := sess.PlanNodeAdd(0, "step one"); err != nil {
		t.Fatalf("PlanNodeAdd: %v", err)
	}
	if _, err := sess.Update(TurnEnd{Input: "in", Output: "out",
		Outcome: "answered", CreatedAt: turnStamp}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	imp, err := sess.ImportL3([]L3ImportItem{{Title: "auth", Domain: "proj",
		NodeType: "package", Content: "who logs in"}}, L3ImportOverwrite)
	check("ImportL3", imp, err)
	reads(true)
}

// walkNils records every path whose slice or map is nil. A pointer field that is unset is a
// documented absence (ParentID, the anchor) and is not a collection, so it is not reported.
func walkNils(v reflect.Value, path string, out *[]string) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			walkNils(v.Elem(), path, out)
		}
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			*out = append(*out, path)
			return
		}
		for i := 0; i < v.Len(); i++ {
			walkNils(v.Index(i), fmt.Sprintf("%s[%d]", path, i), out)
		}
	case reflect.Map:
		if v.IsNil() {
			*out = append(*out, path)
			return
		}
		for _, k := range v.MapKeys() {
			val := v.MapIndex(k)
			if val.Kind() == reflect.Interface && !val.IsNil() {
				val = val.Elem()
			}
			walkNils(val, fmt.Sprintf("%s[%v]", path, k.Interface()), out)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			walkNils(v.Field(i), path+"."+v.Type().Field(i).Name, out)
		}
	}
}
