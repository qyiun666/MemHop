// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// A host reading this surface builds one table of key names and uses it everywhere: the
// arguments of a tool call, the JSON it echoes into an event, the diff it logs between two
// reads. That only holds if every shape crossing the boundary spells its fields the same
// way, so the rule is machine-checked from the method set outward — walk what every
// exported method takes and returns, and every struct field reachable from there has to
// carry a snake_case key. A field added without one passes `go vet` and every test, and
// shows up as a host's PascalCase key in a tool schema.
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// hiddenWithReason is the whole allowance for a json:"-" on a host-facing shape: one
// derived value that the engine recomputes on every read and refuses to persist, so the
// axes stay the only fact on disk and the word can never drift from them. A host that
// wants it in prose gets it in ProfileBrief ("mbti: ESTP"), which is the shape the loop
// injects anyway.
var hiddenWithReason = map[string]string{
	"MBTIScore.Type": "mbti-hidden-derivation",
}

// guideMentionsReason looks for the marker both guides carry next to the sentence that
// explains the field, so the code cannot keep an exception the docs stopped explaining.
func guideMentionsReason(marker string) bool {
	for _, name := range []string{"../INTEGRATION_GUIDE.md", "../INTEGRATION_GUIDE.zh.md"} {
		raw, err := os.ReadFile(name)
		if err == nil && strings.Contains(string(raw), marker) {
			return true
		}
	}
	return false
}

func TestEveryHostFacingFieldCarriesASnakeCaseKey(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(ty reflect.Type, where string)
	check := func(ty reflect.Type, where string) {
		for ty.Kind() == reflect.Pointer || ty.Kind() == reflect.Slice ||
			ty.Kind() == reflect.Array || ty.Kind() == reflect.Map {
			ty = ty.Elem()
		}
		if ty.Kind() != reflect.Struct || seen[ty] {
			return
		}
		seen[ty] = true
		if ty.Name() != "" {
			where = ty.Name()
		}
		for f := 0; f < ty.NumField(); f++ {
			field := ty.Field(f)
			if !field.IsExported() {
				continue
			}
			location := where + "." + field.Name
			tag, ok := field.Tag.Lookup("json")
			switch {
			case !ok:
				t.Errorf("%s has no json tag: a host sees the Go field name where every other key is snake_case", location)
			case tag == "-":
				// A field hidden from the wire is legal only with a reason on file, and the
				// reason has to be in the guide a host reads: "the key is missing" is
				// otherwise indistinguishable from "this value does not exist".
				hidden := ty.Name() + "." + field.Name
				reason, ok := hiddenWithReason[hidden]
				if !ok {
					t.Errorf("%s is hidden from the wire (json %q) with no recorded reason", location, tag)
				} else if !guideMentionsReason(reason) {
					t.Errorf("the reason recorded for %s is not in either guide: %q", hidden, reason)
				}
			case !keyPattern.MatchString(fieldName(tag)):
				t.Errorf("%s carries the key %q, want a snake_case name", location, tag)
			}
			walk(field.Type, location)
		}
	}
	walk = func(ty reflect.Type, where string) {
		check(ty, where)
	}

	// The pointer type: every method here is declared on *Session / *DB, and a value
	// type's method set would come back empty — which is how this walk could have
	// looked green while checking nothing at all.
	roots := []reflect.Type{reflect.TypeOf(&Session{}), reflect.TypeOf(&DB{})}
	for _, ty := range roots {
		for m := 0; m < ty.NumMethod(); m++ {
			method := ty.Method(m)
			if !method.IsExported() {
				continue
			}
			site := ty.Name() + "." + method.Name
			fn := method.Type
			for i := 0; i < fn.NumIn(); i++ {
				walk(fn.In(i), site+"(arg)")
			}
			for i := 0; i < fn.NumOut(); i++ {
				walk(fn.Out(i), site+"(return)")
			}
		}
	}
	if len(seen) < 15 {
		t.Fatalf("the walk reached %d shapes, want the whole host-facing set (a shallow walk proves nothing)", len(seen))
	}
}

// fieldName takes the key out of a json tag, dropping the options after the comma.
func fieldName(tag string) string {
	if i := strings.IndexByte(tag, ','); i >= 0 {
		return tag[:i]
	}
	return tag
}

func stripOmitempty(tag string) string {
	for i := 0; i < len(tag); i++ {
		if tag[i] == ',' {
			return tag[:i]
		}
	}
	return tag
}
