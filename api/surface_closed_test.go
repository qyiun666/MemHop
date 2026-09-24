// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// A host that shuts down while a worker is still driving a turn is the one race every
// method has to survive, and it can be lost silently: a use-after-close reads as a corrupt
// record, or a nil dereference takes the process down. So the whole published surface is
// walked here, once per method, and the promise is the boring one — every call answers
// `ErrClosed`, the two accessors that read no engine state answer normally, and nothing
// panics. The table is compared against the reflected method sets, so a method cannot be
// added to the surface without being added to this gate.

package api

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// readsNoState names the calls that answer a handle alone: a closed database still knows
// which domain its session holds and whether it was closed.
var readsNoState = map[string]bool{"Session.AgentID": true, "DB.IsClosed": true}

// exemptFromClosedWalk is the one published call this walk must not make, with the reason.
var exemptFromClosedWalk = map[string]string{
	"DB.CompactTo": "its argument is an arbitrary write path; a test gate has no business creating files",
}

func TestEveryCallAnswersErrClosedAfterClose(t *testing.T) {
	lib, sess, _ := openSurfaceLibrary(t)
	res, gid := importGraph(t, sess, L3ImportMerge)
	nodeID := res.CreatedIDs[0]
	searched, err := sess.Search(SearchQuery{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	sceneID, topicID := searched.Scene.SceneID, searched.NewTopicID
	if _, err := sess.AppendArchive(ArchiveInput{
		Kind: KindUtterance, Role: uint8(RoleUser), ContentType: ContentText,
		Content: "hello", CreatedAt: 1_770_000_000_000,
	}); err != nil {
		t.Fatalf("AppendArchive: %v", err)
	}
	if _, err := sess.PlanNodeAdd(0, "step one"); err != nil {
		t.Fatalf("PlanNodeAdd: %v", err)
	}
	if err := lib.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	calls := closedSurface(sess, lib, gid, nodeID, sceneID, topicID)
	names := make([]string, 0, len(calls))
	for name := range calls {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		run := calls[name]
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("%s panicked on a closed database: %v", name, p)
				}
			}()
			err := run()
			if readsNoState[name] {
				if err != nil {
					t.Errorf("%s: a handle accessor answered %v on a closed database", name, err)
				}
				return
			}
			// DB.Close is in this set on purpose: closing an already-closed database answers
			// the same code every other call does, so a host that defers Close and also calls
			// it at shutdown reads one vocabulary, not two.
			if CodeOf(err) != ErrClosed {
				t.Errorf("%s: want ErrClosed (%d), got code=%d err=%v", name, ErrClosed, CodeOf(err), err)
			}
		}()
	}

	if missing := unlistedClosedCalls(names, publishedMethodNames()); len(missing) > 0 {
		t.Fatalf("published methods this walk never calls: %v — the table did not grow with the surface", missing)
	}
	if len(names) < 30 {
		t.Fatalf("the walk covers %d calls, want the whole published surface", len(names))
	}
}

// unlistedClosedCalls reports published "Type.Method" names the walk never calls.
func unlistedClosedCalls(called []string, published map[string]bool) []string {
	seen := make(map[string]bool, len(called))
	for _, c := range called {
		seen[c] = true
	}
	var missing []string
	for name := range published {
		if _, ok := exemptFromClosedWalk[name]; !ok && !seen[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// publishedMethodNames reflects the two handles' exported method sets.
func publishedMethodNames() map[string]bool {
	out := map[string]bool{}
	for prefix, handle := range map[string]any{"Session": &Session{}, "DB": &DB{}} {
		ty := reflect.TypeOf(handle)
		for m := 0; m < ty.NumMethod(); m++ {
			if ty.Method(m).IsExported() {
				out[prefix+"."+ty.Method(m).Name] = true
			}
		}
	}
	return out
}

// closedSurface is the published surface as thunks, each called with an argument set that
// would succeed on an open database — so the close is the only thing left to make it fail.
func closedSurface(sess *Session, lib *DB, gid, nodeID, sceneID, topicID string) map[string]func() error {
	llm := LlmConfig{APIURL: "http://127.0.0.1:1/v1", APIKey: "k", Model: "m"}
	return map[string]func() error{
		"Session.Search":       func() error { _, e := sess.Search(SearchQuery{}); return e },
		"Session.SceneContext": func() error { _, e := sess.SceneContext(""); return e },
		"Session.GetL0":        func() error { _, e := sess.GetL0(); return e },
		"Session.UpdateL0":     func() error { return sess.UpdateL0(&ProfileInput{Name: "p"}) },
		"Session.ListL1":       func() error { _, e := sess.ListL1(); return e },
		"Session.ListScenes":   func() error { _, e := sess.ListScenes(""); return e },
		"Session.UpdateScene":  func() error { _, e := sess.UpdateScene(sceneID, ScenePatch{}); return e },
		"Session.MergeScenes":  func() error { return sess.MergeScenes(sceneID, []string{sceneID}) },
		"Session.DeleteScene":  func() error { return sess.DeleteScene(sceneID) },
		"Session.DeleteTopic":  func() error { return sess.DeleteTopic(topicID) },
		"Session.RenameTopic":  func() error { _, e := sess.RenameTopic(topicID, "x"); return e },
		"Session.GetL3":        func() error { _, e := sess.GetL3(gid); return e },
		"Session.ListL3":       func() error { _, e := sess.ListL3(); return e },
		"Session.ImportL3": func() error {
			_, e := sess.ImportL3([]L3ImportItem{{Title: "n", Domain: "d"}}, L3ImportSkip)
			return e
		},
		"Session.UpdateL3": func() error { _, e := sess.UpdateL3(gid, ptr("other")); return e },
		"Session.DeleteL3": func() error { return sess.DeleteL3(gid) },
		"Session.QueryL3Nodes": func() error {
			_, e := sess.QueryL3Nodes(L3NodeQuery{GraphID: gid})
			return e
		},
		"Session.QueryL3Subgraph": func() error { _, e := sess.QueryL3Subgraph(gid, nodeID, 1, nil); return e },
		"Session.SearchL4":        func() error { _, e := sess.SearchL4(L4Query{}); return e },
		"Session.AppendArchive": func() error {
			_, e := sess.AppendArchive(ArchiveInput{Kind: KindEvent, EventType: "x", CreatedAt: 1_770_000_000_000})
			return e
		},
		"Session.Update": func() error {
			_, e := sess.Update(TurnEnd{Input: "a", Output: "b", CreatedAt: 1_770_000_000_000})
			return e
		},
		"Session.Dream":          func() error { _, e := sess.Dream(context.Background(), ""); return e },
		"Session.PlanNodeAdd":    func() error { _, e := sess.PlanNodeAdd(0, "s"); return e },
		"Session.PlanNodeUpdate": func() error { return sess.PlanNodeUpdate(PlanStep{Seq: 1, Status: PlanStatusDone}) },
		"Session.PlanState":      func() error { _, e := sess.PlanState(); return e },
		"Session.AgentID":        func() error { _ = sess.AgentID(); return nil },
		"DB.Stats":               func() error { _, e := lib.Stats(); return e },
		"DB.Checkpoint":          func() error { return lib.Checkpoint() },
		"DB.Primary":             func() error { _, e := lib.Primary(); return e },
		"DB.SubAgent":            func() error { _, e := lib.SubAgent(llm, ProfileInput{Name: "sub"}); return e },
		"DB.Agent":               func() error { _, e := lib.Agent(llm, "0000000000000001"); return e },
		"DB.Agents":              func() error { _, e := lib.Agents(); return e },
		"DB.IsClosed":            func() error { _ = lib.IsClosed(); return nil },
		"DB.Close":               func() error { return lib.Close() },
	}
}
