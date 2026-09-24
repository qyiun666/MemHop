// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package api

import (
	"fmt"
	"sync"
	"testing"
)

// L3 is the one layer a file shares across every domain: a family of agents reads one
// project graph as a tool, and each of them may be the first to write a node into it. The
// graph's identity is derived from the label the host types, so four domains asking for
// "engine" at once are asking for the same record — which makes this the only place where
// "same id, two writers" is the ordinary case rather than a corner. The id a node derives
// from its title has the same property inside one batch.
//
// Shared identity is two judgements, not one hash formula: a repeat title is settled by the
// graph's own title set long before an address is computed, and the pool's address guard is the
// last line refusing to overwrite a record that is already there. Changing how a node's id is
// derived therefore cannot duplicate a title on its own — which is what the assertions below
// must be read against.
//
// So the outcomes a host can see have to be single-valued: every caller names the same graph,
// the file holds one slot for it, every node that was asked for is there exactly once, and a
// read running beside the storm never fails and never shows a node nobody imported. Run it
// under -race.
func TestSharedKnowledgeGraphUnderConcurrentDomains(t *testing.T) {
	m, _, stubURL := openSurfaceLibrary(t)
	defer func() { _ = m.Close() }()

	const extraDomains = 4
	const perDomain = 5
	sessions := make([]*Session, 0, extraDomains+1)
	primary, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	sessions = append(sessions, primary)
	for i := 0; i < extraDomains; i++ {
		sess, err := m.SubAgent(surfaceLLM(stubURL), ProfileInput{Name: fmt.Sprintf("graph-writer-%d", i)})
		if err != nil {
			t.Fatalf("SubAgent %d: %v", i, err)
		}
		sessions = append(sessions, sess)
	}

	graphIDs := make([]string, len(sessions))
	importErrs := make([]string, len(sessions))
	readErrs := make([]string, 2)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := range readErrs {
		wg.Add(1)
		go func(who int, sess *Session) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// A reader names the graph the way a tool does: it lists, then reads. An
				// empty listing is a legitimate answer before the first import has landed;
				// a slot that lists but does not read, or a node nobody imported, is not.
				graphs, err := sess.ListL3()
				if err != nil {
					readErrs[who] = fmt.Sprintf("reader %d: ListL3: %v", who, err)
					return
				}
				if len(graphs) > 1 {
					readErrs[who] = fmt.Sprintf("reader %d lists %d graphs while every domain imports one label",
						who, len(graphs))
					return
				}
				if len(graphs) == 0 {
					continue
				}
				id := graphs[0].ID
				graph, err := sess.GetL3(id)
				if err != nil {
					readErrs[who] = fmt.Sprintf("reader %d: the graph it was just handed does not read: %v", who, err)
					return
				}
				if len(graph.Nodes) > len(sessions)*perDomain+1 {
					readErrs[who] = fmt.Sprintf("reader %d sees %d nodes, more than anyone imported",
						who, len(graph.Nodes))
					return
				}
				if _, err := sess.QueryL3Nodes(L3NodeQuery{GraphID: id, Keyword: "content"}); err != nil {
					readErrs[who] = fmt.Sprintf("reader %d: QueryL3Nodes: %v", who, err)
					return
				}
			}
		}(w, sessions[w])
	}

	var writeWG sync.WaitGroup
	for i, sess := range sessions {
		writeWG.Add(1)
		go func(worker int, sess *Session) {
			defer writeWG.Done()
			items := make([]L3ImportItem, 0, perDomain+1)
			for n := 0; n < perDomain; n++ {
				items = append(items, L3ImportItem{
					Title: fmt.Sprintf("node-%d-%d", worker, n), Domain: "engine",
					NodeType: "concept", Content: "content of a node",
				})
			}
			// Every domain imports this one title too: same label, same title, same derived
			// address, four writers.
			items = append(items, L3ImportItem{
				Title: "shared", Domain: "engine", NodeType: "concept", Content: "everyone's node",
			})
			res, err := sess.ImportL3(items, L3ImportSkip)
			if err != nil {
				importErrs[worker] = fmt.Sprintf("import: %v", err)
				return
			}
			if len(res.Errors) != 0 {
				importErrs[worker] = fmt.Sprintf("import reported per-item failures: %v", res.Errors)
				return
			}
			if len(res.GraphIDs) != 1 {
				importErrs[worker] = fmt.Sprintf("one batch over one label named %d graphs", len(res.GraphIDs))
				return
			}
			graphIDs[worker] = res.GraphIDs[0]
		}(i, sess)
	}
	writeWG.Wait()
	close(stop)
	wg.Wait()

	for _, failure := range append(importErrs, readErrs...) {
		if failure != "" {
			t.Fatalf("%s", failure)
		}
	}
	for i, id := range graphIDs {
		if id != graphIDs[0] {
			t.Fatalf("writer %d was handed graph %s while writer 0 got %s — the same label produced two "+
				"graphs, so each domain is now looking at a different project", i, id, graphIDs[0])
		}
	}
	graphs, err := primary.ListL3()
	if err != nil {
		t.Fatalf("ListL3: %v", err)
	}
	if len(graphs) != 1 {
		t.Fatalf("the file holds %d graph slots after every domain imported the label \"engine\": %+v",
			len(graphs), graphs)
	}

	// The answer a tool call gives must be the set the domains imported: one row per distinct
	// title, neither fewer (a lost write) nor twice (two records for one address).
	graph, err := primary.GetL3(graphIDs[0])
	if err != nil {
		t.Fatalf("GetL3 after the storm: %v", err)
	}
	byTitle := map[string]int{}
	for _, n := range graph.Nodes {
		byTitle[n.Title]++
	}
	if want := len(sessions)*perDomain + 1; len(graph.Nodes) != want {
		t.Fatalf("the graph reads %d nodes, want %d: %+v", len(graph.Nodes), want, graph.Nodes)
	}
	for title, count := range byTitle {
		if count > 1 {
			t.Fatalf("title %q is on the graph %d times after %d domains imported it", title, count, len(sessions))
		}
	}
	for w := 0; w < len(sessions); w++ {
		for n := 0; n < perDomain; n++ {
			title := fmt.Sprintf("node-%d-%d", w, n)
			if byTitle[title] != 1 {
				t.Fatalf("%q is not on the graph (%d copies): a concurrent import was lost", title, byTitle[title])
			}
		}
	}
	if byTitle["shared"] != 1 {
		t.Fatalf("the title every domain imported reads %d copies, want one", byTitle["shared"])
	}
}
