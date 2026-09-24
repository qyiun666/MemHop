// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The other half of the deployment the host actually builds: one process, several agents, each
// behind the same two-method port from `api_interface_memory_port_test.go` — some sharing one
// file as separate domains, one spawned per task with a file of its own. This is where an
// integrator learns the rules that are invisible from a single-session adapter: a sub-agent is
// a domain, not a database, so its rounds never appear in the parent's recall and the other way
// round; a `.meh` is opened by one instance at a time, so reading what a finished worker left
// behind means reopening its file rather than holding two handles; and everything a fresh
// adapter needs is in the file, because the adapter was never given an id to keep.

package test

import (
	"path/filepath"
	"strings"
	"testing"

	memhop "github.com/qyiun666/MemHop/api"
)

func TestInterfaceMemoryPortServesSeveralAgentsOnOneFileAndOneFileEach(t *testing.T) {
	llm := newMockLLM(t)
	dir := t.TempDir()
	parentPath := filepath.Join(dir, "parent.meh")
	workerPath := filepath.Join(dir, "worker.meh")

	parentDB := openMockDB(t, parentPath, llm.srv.URL)
	parent := &memoryPort{sess: newTestDB(t, parentDB).Session}
	// A tool call that starts a helper is `SubAgent` on the same handle: a domain, not a
	// database. The adapter shape does not change — it still holds one session.
	helperDB := mustSub(t, parentDB, llm.srv.URL, "helper")
	helper := &memoryPort{sess: helperDB}
	// A spawned agent for a whole task gets its own file, opened the same way.
	workerDB := openMockDB(t, workerPath, llm.srv.URL)
	worker := &memoryPort{sess: newTestDB(t, workerDB).Session}

	runOne := func(p *memoryPort, cue, reply string) {
		t.Helper()
		if err := p.begin(); err != nil {
			t.Fatalf("begin for %s: %v", cue, err)
		}
		if _, err := p.recall(); err != nil {
			t.Fatalf("recall for %s: %v", cue, err)
		}
		if err := p.remember(cue, reply, "succeeded"); err != nil {
			t.Fatalf("remember for %s: %v", cue, err)
		}
	}
	if parent.sess.AgentID() == helper.sess.AgentID() {
		t.Fatalf("a sub-agent shares the parent's domain id %s, so two adapters would silently "+
			"write one memory", parent.sess.AgentID())
	}

	runOne(parent, "父任务的第一句", "父答案")
	runOne(helper, "帮手的唯一一句", "助手答案")
	runOne(parent, "父任务的第二句", "父答案二")
	runOne(worker, " worker 独立的一句", "worker 答案")

	// Same file, different domains: neither side sees the other's rows, and the parent's count is
	// its own two rounds rather than the three written into that file.
	if rows := strings.Join(recalled(t, parent), "\n"); strings.Count(rows, "round:") != 2 {
		t.Fatalf("the parent recalled %d rounds from a file holding three, want its own two:\n%s",
			strings.Count(rows, "round:"), rows)
	}
	if rows := strings.Join(recalled(t, helper), "\n"); strings.Count(rows, "round:") != 1 {
		t.Fatalf("the helper recalled %d rounds, want its single one:\n%s",
			strings.Count(rows, "round:"), rows)
	}
	// The worker is a separate database: nothing crosses, in either direction.
	if rows := strings.Join(recalled(t, worker), "\n"); strings.Count(rows, "round:") != 1 {
		t.Fatalf("the worker recalled %d rounds, want its single one:\n%s",
			strings.Count(rows, "round:"), rows)
	}

	// A second instance cannot be opened against a file some handle already holds: the exclusive
	// lock is what makes one file one memory, and it is the reason a host keys memories by path
	// across files and by id only inside one.
	if peeker, err := memhop.Open(workerPath, testLLM(llm.srv.URL), memhop.MemHopDefaults{},
		&memhop.ProfileInput{Name: "peeker"}); err == nil {
		_ = peeker.Close()
		t.Fatal("a second instance was allowed against a file another handle still holds")
	} else if code := memhop.CodeOf(err); code != memhop.ErrIO ||
		!strings.Contains(err.Error(), "another instance") {
		t.Fatalf("the refused second open answers code %d (%v); a host decides whether to retry by "+
			"that pairing — ErrIO alone cannot tell a busy file from a broken disk", code, err)
	}

	// Reading what the worker left behind therefore takes a reopen, not a second handle.
	if err := workerDB.Close(); err != nil {
		t.Fatalf("worker close: %v", err)
	}
	inspector := &memoryPort{sess: newTestDB(t, openMockDB(t, workerPath, llm.srv.URL)).Session}
	rows := strings.Join(recalled(t, inspector), "\n")
	if strings.Count(rows, "round:") != 1 {
		t.Fatalf("reopening the worker's file recalled %d rounds, want the one it settled:\n%s",
			strings.Count(rows, "round:"), rows)
	}
	if strings.Contains(rows, "父答案") || strings.Contains(rows, "助手答案") {
		t.Fatalf("the worker's file carried the parent domain's rows after a restart:\n%s", rows)
	}
}

// recalled reads through the adapter the way a framework would before a model call.
func recalled(t *testing.T, p *memoryPort) []string {
	t.Helper()
	rows, err := p.recall()
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	return rows
}
