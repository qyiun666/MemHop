// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The public surface of the facade, pinned by reflection: these lists are the review
// gate, so nothing becomes host-callable without an edit here. Add a name only when
// the method is meant to be public. TestPublicSignaturesCarryNoNumericIds and
// TestHandlesExposeNoField below enforce the other half of the contract — no id a host
// can see leaves as a number, and no field of a handle is reachable around its
// methods.

package api

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func methodNames(v any) []string {
	t := reflect.TypeOf(v)
	out := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		out = append(out, t.Method(i).Name)
	}
	sort.Strings(out)
	return out
}

func diffNames(want, got []string) (missing, extra []string) {
	have := make(map[string]struct{}, len(got))
	for _, n := range got {
		have[n] = struct{}{}
	}
	wanted := make(map[string]struct{}, len(want))
	for _, n := range want {
		wanted[n] = struct{}{}
	}
	for _, n := range got {
		if _, ok := wanted[n]; !ok {
			extra = append(extra, n)
		}
	}
	for _, n := range want {
		if _, ok := have[n]; !ok {
			missing = append(missing, n)
		}
	}
	return missing, extra
}

func TestSessionPublicSurface(t *testing.T) {
	want := []string{
		// runtime/task face — the host drives these every turn and LLM tools
		// bind to them
		// core cycle (host-driven)
		"Search", "Settle", "Dream", "AppendArchive",
		// L0 profile
		"GetL0", "UpdateL0",
		// L1 scene hypergraph (read-only; Dream is the only writer)
		"ListL1",
		// L2 scene reads
		"ListScenes", "SceneContext",
		// L3 knowledge
		"GetL3", "ListL3", "ImportL3", "QueryL3Nodes", "QueryL3Subgraph",
		// L4 archives
		"SearchL4",
		// the plan tree: written one step at a time — one step created or
		// restated per call (parentSeq 0 opens the tree)
		"PlanNodeAdd", "PlanNodeUpdate", "PlanState",

		// assembly/admin face — host code at session boundaries and management
		// channels only, never an LLM tool
		// L2 scene management and corrections
		"UpdateScene", "RenameTopic", "MergeScenes", "DeleteScene", "DeleteTopic",
		// L3 management and corrections
		"UpdateL3", "DeleteL3",
	}
	sort.Strings(want)

	missing, extra := diffNames(want, methodNames(&Session{}))
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("Session public surface drifted: missing=%v unexpected=%v", missing, extra)
	}
}

func TestDBPublicSurface(t *testing.T) {
	// Two ways in — the domain the file was opened on, and one created under it
	// by name — plus the file-level lifecycle and the file-level diagnostics. No
	// agent id crosses either, and no LLM tool binds here.
	want := []string{
		"Primary", "SubAgent",
		"Checkpoint", "CompactTo", "Close", "IsClosed", "Stats",
	}
	sort.Strings(want)

	missing, extra := diffNames(want, methodNames(&DB{}))
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("DB public surface drifted: missing=%v unexpected=%v", missing, extra)
	}
}

// TestHandlesExposeNoField pins the route the method lists cannot see: an exported
// field on a handle lets a host reach the internal value it holds — and with it the
// numeric ids this facade renders as hex — without calling a single method. Both
// handles carry their counterpart unexported.
func TestHandlesExposeNoField(t *testing.T) {
	for _, handle := range []struct {
		name string
		v    any
	}{
		{"Session", &Session{}},
		{"DB", &DB{}},
	} {
		typ := reflect.TypeOf(handle.v).Elem()
		for i := 0; i < typ.NumField(); i++ {
			if f := typ.Field(i); f.PkgPath == "" {
				t.Errorf("%s.%s is exported: a host reaches the internal value around every method here",
					handle.name, f.Name)
			}
		}
	}
}

// TestPublicSignaturesCarryNoNumericIds pins the other half of the facade
// contract: every id a host can see is a 16-char hex string. Input-only types
// are deliberately aliases of their internal seam (SearchQuery, L4Query…), so the
// package path is not what matters — a uint64 field is.
// UpdateScene is the shape to watch: without its facade override the result is a
// core.SceneSlot, whose SceneID and L3ID are uint64.
func TestPublicSignaturesCarryNoNumericIds(t *testing.T) {
	for _, handle := range []struct {
		name string
		v    any
	}{
		{"Session", &Session{}},
		{"DB", &DB{}},
	} {
		typ := reflect.TypeOf(handle.v)
		for i := 0; i < typ.NumMethod(); i++ {
			m := typ.Method(i)
			for side, types := range map[string][]reflect.Type{"param": inTypes(m.Type), "result": outTypes(m.Type)} {
				for _, lt := range types {
					if bad := numericIDField(lt, map[reflect.Type]bool{}); bad != "" {
						t.Errorf("%s.%s exposes %s — render it as a hex string in the facade",
							handle.name, m.Name, side+" "+bad)
					}
				}
			}
		}
	}
}

// inTypes skips In(0): for a method read off a type, that first input is the
// receiver, whose struct graph reaches the storage engine.
func inTypes(fn reflect.Type) []reflect.Type {
	out := make([]reflect.Type, 0, fn.NumIn())
	for i := 1; i < fn.NumIn(); i++ {
		out = append(out, fn.In(i))
	}
	return out
}

func outTypes(fn reflect.Type) []reflect.Type {
	out := make([]reflect.Type, 0, fn.NumOut())
	for i := 0; i < fn.NumOut(); i++ {
		out = append(out, fn.Out(i))
	}
	return out
}

// numericIDField walks a type (and every struct it can reach) looking for a
// uint64 field named like an identifier — the shape an internal record id takes
// before the facade renders it. Counters (a turn's slot ordinal, a plan step's
// Seq) share the width and are fine to hand out. seen breaks recursive types.
func numericIDField(t reflect.Type, seen map[reflect.Type]bool) string {
	if t == nil || seen[t] {
		return ""
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return numericIDField(t.Elem(), seen)
	case reflect.Map:
		if bad := numericIDField(t.Key(), seen); bad != "" {
			return bad
		}
		return numericIDField(t.Elem(), seen)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue // unexported: a host cannot reach it, embedded handles included
			}
			if f.Type.Kind() == reflect.Uint64 && strings.Contains(f.Name, "ID") {
				return t.Name() + "." + f.Name + " is a uint64 id"
			}
			if bad := numericIDField(f.Type, seen); bad != "" {
				return t.Name() + "." + bad
			}
		}
	}
	return ""
}
