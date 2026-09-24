// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"context"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// A host wiring these calls to a model writes a tool schema per method: a name, the keys to
// ask for, and what the answer is good for. Both guides carry that table (§7.7), and a table
// nothing checks is a table that rots — so the keys are read back out of the facade's own
// struct tags, the width out of its method set, and the guides are held against both.
//
// The admin-face methods are deliberately absent from that table: a model holding a delete or
// a merge needs the host's approval path, not a schema.
type toolRow struct {
	method   string
	tool     string
	argc     int            // arguments the method really takes (receiver and context aside)
	keyTypes []reflect.Type // shapes whose json keys the row must list
	keyNames []string       // other names the row must list, in the guide's words
}

var toolRows = []toolRow{
	{"Search", "memory_search", 1, []reflect.Type{reflect.TypeOf(SearchQuery{})}, nil},
	{"AppendArchive", "memory_record", 1, []reflect.Type{reflect.TypeOf(ArchiveInput{})}, nil},
	{"Update", "memory_close_turn", 1, []reflect.Type{reflect.TypeOf(TurnEnd{})}, nil},
	{"Dream", "memory_dream", 1, nil, []string{"scene_id"}},
	{"GetL0", "memory_profile_get", 0, nil, nil},
	{"UpdateL0", "memory_profile_update", 1, []reflect.Type{reflect.TypeOf(ProfileInput{})}, nil},
	{"ListL1", "memory_associations", 0, nil, nil},
	{"ListScenes", "memory_scenes", 1, nil, []string{"l3_id"}},
	{"SceneContext", "memory_scene_read", 1, nil, []string{"scene_id"}},
	{"GetL3", "memory_graph_get", 1, nil, []string{"id"}},
	{"ListL3", "memory_graph_list", 0, nil, nil},
	{"ImportL3", "memory_graph_import", 2,
		[]reflect.Type{reflect.TypeOf(L3ImportItem{}), reflect.TypeOf(L3Relation{})},
		[]string{"items", "mode"}},
	{"QueryL3Nodes", "memory_nodes_query", 1, []reflect.Type{reflect.TypeOf(L3NodeQuery{})}, nil},
	{"QueryL3Subgraph", "memory_subgraph", 4, nil,
		[]string{"graph_id", "start_node_id", "max_depth", "edge_kinds"}},
	{"SearchL4", "memory_archive_search", 1, []reflect.Type{reflect.TypeOf(L4Query{})}, nil},
	{"PlanNodeAdd", "plan_add_step", 2, nil, []string{"parent_seq", "title"}},
	{"PlanNodeUpdate", "plan_update_step", 1, []reflect.Type{reflect.TypeOf(PlanStep{})}, nil},
	{"PlanState", "plan_state", 0, nil, nil},
}

// adminFace is the other half of the session surface; none of it belongs in the tool table.
var adminFace = []string{
	"UpdateScene", "RenameTopic", "MergeScenes", "DeleteScene", "DeleteTopic",
	"UpdateL3", "DeleteL3", "AgentID",
}

func TestToolTableMatchesTheGuide(t *testing.T) {
	for _, path := range []string{"../INTEGRATION_GUIDE.md", "../INTEGRATION_GUIDE.zh.md"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		section := toolSection(t, path, string(raw))

		for _, row := range toolRows {
			line := rowLine(t, section, row.method, path)
			if !hasWord(line, "`"+row.tool+"`") {
				t.Errorf("%s: the row for Session.%s offers no tool name %q", path, row.method, row.tool)
			}
			for _, key := range row.keys() {
				if !hasWord(line, "`"+key+"`") {
					t.Errorf("%s: Session.%s needs %q, which its tool row does not ask the model for",
						path, row.method, key)
				}
			}
		}
		for _, method := range adminFace {
			if strings.Contains(section, "`Session."+method+"`") {
				t.Errorf("%s: the admin-face method %s appears in the tool table at all", path, method)
			}
		}
		// The paragraph that keeps a model off the clock has to stay where the table is.
		if !hasWord(section, "`host-filled-keys`") {
			t.Errorf("%s: the tool table no longer says which keys the host fills itself", path)
		}
		for _, key := range []string{"created_at", "seq"} {
			if !hasWord(section, "`"+key+"`") {
				t.Errorf("%s: host-filled key %q is no longer named next to the table", path, key)
			}
		}
		if got := tableRows(section); got != len(toolRows) {
			t.Errorf("%s: the tool table has %d rows, want one per task-face method (%d)",
				path, got, len(toolRows))
		}
	}
}

// TestEveryTaskFaceMethodHasAToolRow closes the other direction: a method added to the
// session surface is only absent from the tool table on purpose. Without this check the
// table could quietly cover 18 of 19 task-face calls, and the host reading it would have
// no reason to suspect the nineteenth exists.
func TestEveryTaskFaceMethodHasAToolRow(t *testing.T) {
	listed := map[string]bool{}
	for _, row := range toolRows {
		listed[row.method] = true
	}
	admin := map[string]bool{}
	for _, method := range adminFace {
		admin[method] = true
	}
	handle := reflect.TypeOf(&Session{})
	for i := 0; i < handle.NumMethod(); i++ {
		method := handle.Method(i)
		if !method.IsExported() || admin[method.Name] {
			continue
		}
		if !listed[method.Name] {
			t.Errorf("Session.%s is exported and not admin-face, so §7.7 owes it a tool row", method.Name)
		}
	}
}

// TestToolRowsFollowTheMethodSet checks the same table against the code: every listed method
// exists and takes what the row says it takes, so a signature change cannot keep a table that
// still reads correctly.
func TestToolRowsFollowTheMethodSet(t *testing.T) {
	handle := reflect.TypeOf(&Session{})
	for _, row := range toolRows {
		method, ok := handle.MethodByName(row.method)
		if !ok {
			t.Fatalf("Session.%s is gone, so the tool table names a call that no longer exists", row.method)
		}
		if got := len(methodArgs(method.Type)); got != row.argc {
			t.Errorf("Session.%s takes %d arguments, the table describes %d", row.method, got, row.argc)
		}
	}
}

// keys returns every name a model has to be asked for: the row's own words plus the json keys
// of each shape it names (those tags are the contract; see surface_keys_test.go).
func (r toolRow) keys() []string {
	out := append([]string{}, r.keyNames...)
	for _, ty := range r.keyTypes {
		for f := 0; f < ty.NumField(); f++ {
			field := ty.Field(f)
			tag, ok := field.Tag.Lookup("json")
			if !ok {
				continue
			}
			out = append(out, strings.Split(tag, ",")[0])
		}
	}
	return out
}

// methodArgs drops the receiver and any context, which no tool schema asks a model for.
func methodArgs(fn reflect.Type) []reflect.Type {
	var out []reflect.Type
	ctx := reflect.TypeOf((*context.Context)(nil)).Elem()
	for i := 1; i < fn.NumIn(); i++ {
		if fn.In(i) == ctx {
			continue
		}
		out = append(out, fn.In(i))
	}
	return out
}

func toolSection(tb testing.TB, path, text string) string {
	tb.Helper()
	start := strings.Index(text, "\n### 7.7")
	if start < 0 {
		tb.Fatalf("%s has no §7.7 tool table", path)
	}
	rest := text[start:]
	end := strings.Index(rest, "\n## 8")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

func rowLine(tb testing.TB, section, method, path string) string {
	tb.Helper()
	marker := "`Session." + method + "`"
	for _, line := range strings.Split(section, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	tb.Fatalf("%s: no tool row names %s", path, marker)
	return ""
}

func tableRows(section string) int {
	n := 0
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "| `") {
			n++
		}
	}
	return n
}

// hasWord matches a whole token: a guide that wrote `related` inside a longer identifier does
// not yet tell a host that the key is `related`.
func hasWord(line, word string) bool {
	return regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(word) + `($|[^A-Za-z0-9_])`).MatchString(line)
}
