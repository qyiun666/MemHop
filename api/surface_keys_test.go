// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"os"
	"reflect"
	"regexp"
	"sort"
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

// fieldTablesInGuides binds each guide table that enumerates a shape's keys to that shape, by the
// marker the section carries (both languages carry the same marker, so one entry covers the pair).
// The snake_case walk above only proves a key is spelled the way keys are spelled; this proves the
// list is that shape's: a row naming a key the shape does not emit sends a host reading a field
// that is never there, and a key the shape emits with no row is one it only finds in `go doc`.
var fieldTablesInGuides = map[string]any{
	"l1-node-fields": SceneNodeView{},
}

func TestGuideFieldTablesEnumerateTheShapeExactly(t *testing.T) {
	for marker, shape := range fieldTablesInGuides {
		want := jsonKeys(shape)
		for _, guide := range []string{"../INTEGRATION_GUIDE.md", "../INTEGRATION_GUIDE.zh.md"} {
			raw, err := os.ReadFile(guide)
			if err != nil {
				t.Fatalf("read guide %s: %v", guide, err)
			}
			cited, ok := tableKeys(string(raw), marker)
			if !ok {
				t.Fatalf("%s has no field table after the %q marker", guide, marker)
			}
			missing, extra := keyDiff(want, cited)
			if len(missing) > 0 || len(extra) > 0 {
				t.Errorf("%s: the table for %s omits %v and names keys the shape does not emit: %v",
					guide, reflect.TypeOf(shape).Name(), missing, extra)
			}
		}
	}
}

// jsonKeys returns the keys a shape emits on the wire, skipping any hidden from it.
func jsonKeys(shape any) []string {
	ty := reflect.TypeOf(shape)
	out := make([]string, 0, ty.NumField())
	for i := 0; i < ty.NumField(); i++ {
		tag, ok := ty.Field(i).Tag.Lookup("json")
		if !ok {
			continue
		}
		if key := fieldName(tag); key != "-" {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

var tableKeyCell = regexp.MustCompile("^`([a-z][a-z0-9_]*)`$")

// tableKeys collects the backticked keys from the first column of the table that follows marker,
// stopping at the next heading so a later table cannot answer for this one. A cell naming a pair of
// keys (`id` / `scene_id`) counts as both.
func tableKeys(text, marker string) ([]string, bool) {
	at := strings.Index(text, marker)
	if at < 0 {
		return nil, false
	}
	found, seen := false, map[string]bool{}
	for _, line := range strings.Split(text[at+len(marker):], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			break
		}
		if !strings.HasPrefix(line, "|") {
			continue
		}
		found = true
		first := strings.TrimSpace(strings.Split(line, "|")[1])
		for _, cell := range strings.Split(first, "/") {
			if m := tableKeyCell.FindStringSubmatch(strings.TrimSpace(cell)); m != nil {
				seen[m[1]] = true
			}
		}
	}
	if !found {
		return nil, false
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, true
}

// keyDiff reports the keys only one side has.
func keyDiff(want, got []string) (missing, extra []string) {
	inGot, inWant := toSet(got), toSet(want)
	for _, k := range want {
		if !inGot[k] {
			missing = append(missing, k)
		}
	}
	for _, k := range got {
		if !inWant[k] {
			extra = append(extra, k)
		}
	}
	return missing, extra
}

func toSet(list []string) map[string]bool {
	out := make(map[string]bool, len(list))
	for _, s := range list {
		out[s] = true
	}
	return out
}

// The same comparison pointed at a table that invented one key and dropped another: a gate that
// cannot name both is a set equality nobody would trust. The invented one is the axis a profile
// carries but an L1 node does not, which is exactly the row a host would believe.
func TestKeyDiffNamesFabricatedAndMissingKeys(t *testing.T) {
	text := "marker\n| Field | What it answers |\n|---|---|\n" +
		"| `id` / `scene_id` | a |\n| `topic_ids` | b |\n| `edge_ids` | c |\n" +
		"| `importance` | d |\n| `valence` / `arousal` / `dominance` | e |\n| `created_at` / `updated_at` | f |\n"
	cited, ok := tableKeys(text, "marker")
	if !ok {
		t.Fatal("the table after the marker was not found")
	}
	missing, extra := keyDiff(jsonKeys(SceneNodeView{}), cited)
	if len(missing) != 1 || missing[0] != "emotion_set" {
		t.Fatalf("missing %v, want [emotion_set]", missing)
	}
	if len(extra) != 1 || extra[0] != "dominance" {
		t.Fatalf("extra %v, want [dominance]", extra)
	}
}
