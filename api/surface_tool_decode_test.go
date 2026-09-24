// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The tool path is where "integrate and use" is decided: a model returns one JSON object
// per call, and if the host has to translate that object before handing it over, every
// schema it publishes is a translation table it must keep in sync. So this decodes the
// argument document straight into each input shape and requires **every field of the shape
// to arrive** by the key the guides document — including the enums in the spelling each of
// them is published in (lowercase strings for `plan_status`/`mode`, numbers for
// `kind`/`content_type`/edge kinds). A field that only fills from a Go literal, or a word
// enum that only decodes as an integer, is exactly the hidden conversion a host would
// discover in production.

package api

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestToolArgumentsDecodeIntoEveryInputShape(t *testing.T) {
	cases := []struct {
		name  string
		doc   string
		into  func() any
		shape any
	}{
		{"memory_search", `{
			"scene_id": "0000000000000001",
			"new_scene": true,
			"l3_id": "0000000000000002"
		}`, func() any { return &SearchQuery{} }, SearchQuery{}},

		{"memory_record", `{
			"kind": 0,
			"seq": 3,
			"content_type": 1,
			"role": 2,
			"event_type": "tool_call",
			"node_seq": 1,
			"created_at": 1770000000000,
			"content": "looked it up"
		}`, func() any { return &ArchiveInput{} }, ArchiveInput{}},

		{"memory_close_turn", `{
			"input": "user asked",
			"output": "agent answered",
			"outcome": "resolved",
			"created_at": 1770000000000
		}`, func() any { return &TurnEnd{} }, TurnEnd{}},

		{"memory_scene_update", `{
			"name": "renamed session",
			"l3_id": "0000000000000002",
			"force": true
		}`, func() any { return &ScenePatch{} }, ScenePatch{}},

		{"memory_archive_search", `{
			"keyword": "auth",
			"start": 1760000000000,
			"end": 1780000000000,
			"type": 0,
			"kind": 1,
			"limit": 10,
			"ids": ["0000000000000003"],
			"topic_id": "0000000000000001",
			"node_seq": 2
		}`, func() any { return &L4Query{} }, L4Query{}},

		{"memory_graph_import_item", `{
			"title": "auth",
			"domain": "proj/pkg",
			"node_type": "package",
			"content": "who logs in",
			"keywords": ["auth", "tokens"],
			"source_ref": "internal/auth.go:12",
			"related": [{"titles": ["token"], "kind": 2}]
		}`, func() any { return &L3ImportItem{} }, L3ImportItem{}},

		{"memory_nodes_query", `{
			"graph_id": "0000000000000002",
			"ids": ["0000000000000003"],
			"keyword": "auth",
			"node_type": "package",
			"limit": 5
		}`, func() any { return &L3NodeQuery{} }, L3NodeQuery{}},

		{"plan_add_or_update_step", `{
			"seq": 2,
			"status": "done",
			"title": "fix the fallback",
			"summary": "moved the retry boundary"
		}`, func() any { return &PlanStep{} }, PlanStep{}},

		{"memory_profile_update", `{
			"name": "guide",
			"role": "assistant",
			"personality": "writes the test first",
			"preferences": {"language": "Go"}
		}`, func() any { return &ProfileInput{} }, ProfileInput{}},
	}

	for _, c := range cases {
		// The shape must accept every key the document sends and send no key the
		// shape does not carry: that pair is the whole "no translation layer" claim.
		var fromDoc map[string]any
		if err := json.Unmarshal([]byte(c.doc), &fromDoc); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		fromShape := map[string]bool{}
		for _, key := range jsonKeys(c.shape) {
			fromShape[key] = true
		}
		for key := range fromDoc {
			if _, ok := fromShape[key]; !ok {
				t.Errorf("%s: the document sends %q, which no field of %T takes — the host would have to rewrite the key",
					c.name, key, c.shape)
			}
		}
		for key := range fromShape {
			if _, ok := fromDoc[key]; !ok {
				t.Errorf("%s: %T carries %q, which no example document ever sends", c.name, c.shape, key)
			}
		}

		target := reflect.New(reflect.TypeOf(c.shape))
		if err := json.Unmarshal([]byte(c.doc), target.Interface()); err != nil {
			t.Fatalf("%s: the model's own argument object does not decode: %v", c.name, err)
		}
		if empty := untouchedFields(target.Elem()); len(empty) > 0 && !onlyZeroValued(c.name, empty) {
			t.Errorf("%s: fields the decode left at their zero value with no explanation: %v", c.name, empty)
		}
	}

	// A few values have to land as the constants, not merely as "something non-zero": the
	// enum spellings are the contract the guides' §7.6 table publishes.
	var step PlanStep
	if err := json.Unmarshal([]byte(`{"seq":1,"status":"in_progress"}`), &step); err != nil || step.Status != PlanStatusInProgress {
		t.Fatalf("plan status did not decode from its word: %v err=%v", step.Status, err)
	}
	var item L3ImportItem
	if err := json.Unmarshal([]byte(`{"title":"a","domain":"d"}`), &item); err != nil || item.Title == "" {
		t.Fatalf("import item did not decode: %v", err)
	}
	var archive ArchiveInput
	if err := json.Unmarshal([]byte(`{"kind":1,"content_type":255}`), &archive); err != nil {
		t.Fatalf("archive kind and content type did not decode from their numbers: %v", err)
	}
	if archive.Kind != KindEvent || archive.ContentType != ContentOther {
		t.Fatalf("numeric enums landed as %v/%v, want event/other", archive.Kind, archive.ContentType)
	}
}

func isZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Pointer:
		return v.IsNil()
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	default:
		return v.IsZero()
	}
}

// untouchedFields names the exported fields a decode left at their zero value.
func untouchedFields(v reflect.Value) []string {
	var out []string
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if isZero(v.Field(i)) {
			out = append(out, fieldName(f.Tag.Get("json")))
		}
	}
	return out
}

// onlyZeroValued excuses the fields whose documented value simply is 0: `kind: 0` is
// utterance and `seq: 0` is "next free slot", so a zero there means the key arrived.
func onlyZeroValued(name string, empty []string) bool {
	allowed := map[string]map[string]bool{
		"memory_record": {"kind": true, "seq": true, "node_seq": true},
	}[name]
	for _, key := range empty {
		if !allowed[key] {
			return false
		}
	}
	return len(empty) > 0
}
