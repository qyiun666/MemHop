// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The documented host loop, run end to end inside this repository alone: open a file, read a
// round into the prompt, plan the round step by step, record what each step did against that
// step, close the round, spawn a second domain in the middle of it all, and run a second
// round. Item 11 of the host's list is a flow rather than a method, and until now no single
// case here ran it: the corpus case never touches the plan face, the plan cases never close a
// turn, and the cross-repo check for this sequence lives outside the repository — so a clone
// had no way to see whether the pieces fit together without building two other projects.
//
// Everything asserted below is read the way a host reads it: hex ids used as the keys they
// are, enum values compared to the constants they print, and a prompt rendered from the
// returned shapes with no translation layer in between.

package test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	memhop "github.com/qyiun666/MemHop/api"
)

// renderPlan is the whole of what a host needs to turn a PlanTree into prompt text: no ids to
// resolve, no status words to map, no numbers to cast.
func renderPlan(t *testing.T, tree memhop.PlanTree) string {
	t.Helper()
	var b strings.Builder
	var walk func([]memhop.PlanNodeView, int)
	walk = func(nodes []memhop.PlanNodeView, depth int) {
		for _, n := range nodes {
			b.WriteString(strings.Repeat("  ", depth) + "- #" + strconv.FormatUint(uint64(n.Seq), 10) +
				" [" + n.Status + "] " + n.Title)
			if n.Summary != "" {
				b.WriteString(" — " + n.Summary)
			}
			b.WriteByte('\n')
			walk(n.Children, depth+1)
		}
	}
	walk(tree.Roots, 0)
	return b.String()
}

func TestInterfaceRoundFlowRunsEndToEnd(t *testing.T) {
	llm := newMockLLM(t)
	m := openMockDB(t, filepath.Join(t.TempDir(), "flow.meh"), llm.srv.URL)
	primary, err := m.Primary()
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}

	// --- round 1: read, plan, record against the plan, close ---
	first, err := primary.Search(memhop.SearchQuery{NewScene: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(first.ProfileBrief, "name: test-primary") {
		t.Fatalf("the round's read carries no profile for the prompt: %q", first.ProfileBrief)
	}
	if first.NewTopicID == "" || first.Scene.SceneID == "" {
		t.Fatalf("the read handed back no keys to work with: %+v", first)
	}
	root, err := primary.PlanNodeAdd(0, "定下读路径")
	if err != nil {
		t.Fatalf("PlanNodeAdd root: %v", err)
	}
	child, err := primary.PlanNodeAdd(root, "读 config.go")
	if err != nil {
		t.Fatalf("PlanNodeAdd child: %v", err)
	}
	stamp := time.Now().UnixMilli()
	if _, err := primary.AppendArchive(memhop.ArchiveInput{
		Kind: memhop.KindEvent, EventType: "read_file", NodeSeq: child,
		Content: "config.go:4 个字段", CreatedAt: stamp,
	}); err != nil {
		t.Fatalf("AppendArchive: %v", err)
	}
	if err := primary.PlanNodeUpdate(memhop.PlanStep{Seq: child, Status: memhop.PlanStatusDone, Summary: "四个字段都是时长"}); err != nil {
		t.Fatalf("PlanNodeUpdate child: %v", err)
	}
	if err := primary.PlanNodeUpdate(memhop.PlanStep{Seq: root, Status: memhop.PlanStatusDone}); err != nil {
		t.Fatalf("PlanNodeUpdate root: %v", err)
	}
	tree, err := primary.PlanState()
	if err != nil {
		t.Fatalf("PlanState: %v", err)
	}
	rendered := renderPlan(t, *tree)
	for _, want := range []string{"#1 [done] 定下读路径", "#2 [done] 读 config.go", "四个字段都是时长", "- #1 [done] 定下读路径 — 四个字段都是时长"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the plan the host renders into the prompt is missing %q:\n%s", want, rendered)
		}
	}
	if tree.DoneCount != 2 || tree.TotalCount != 2 {
		t.Fatalf("the tree reports %d/%d done:\n%s", tree.DoneCount, tree.TotalCount, rendered)
	}

	topic, err := primary.Update(memhop.TurnEnd{
		Input: "读路径怎么走", Output: "mmap，读侧零拷贝", Outcome: "done", CreatedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if topic.ID != first.NewTopicID {
		t.Fatalf("the close settled %s, not the turn the read minted (%s)", topic.ID, first.NewTopicID)
	}
	if len(topic.FusedKeywords) == 0 {
		t.Fatalf("the closed turn carries no keyword track: %+v", topic)
	}

	// The work of that step is readable by the step, keyed by the ids the host already holds.
	kind := memhop.KindEvent
	events, err := primary.SearchL4(memhop.L4Query{TopicID: &first.NewTopicID, Kind: &kind, NodeSeq: child})
	if err != nil || len(events) != 1 {
		t.Fatalf("the step's own event track = %+v err %v", events, err)
	}
	if events[0].NodeSeq != child || events[0].Kind.String() != "event" || events[0].Role.String() != "user" {
		t.Fatalf("the event came back without the attribution it was written with: %+v", events[0])
	}

	// --- a second domain, opened in the middle of the loop, keeps its own memory ---
	worker, err := m.SubAgent(testLLM(llm.srv.URL), memhop.ProfileInput{Name: "worker", Role: "帮手"})
	if err != nil {
		t.Fatalf("SubAgent: %v", err)
	}
	if _, err := worker.Search(memhop.SearchQuery{NewScene: true}); err != nil {
		t.Fatalf("worker Search: %v", err)
	}
	if _, err := worker.Update(memhop.TurnEnd{Input: "帮手的第一轮", Output: "答", CreatedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("worker Update: %v", err)
	}
	workerScenes, err := worker.ListScenes("")
	if err != nil || len(workerScenes) != 1 {
		t.Fatalf("the worker sees %+v scenes err %v", workerScenes, err)
	}
	primaryScenes, err := primary.ListScenes("")
	if err != nil || len(primaryScenes) != 1 || primaryScenes[0].SceneID == workerScenes[0].SceneID {
		t.Fatalf("the two domains share a scene listing: %+v vs %+v (err %v)", primaryScenes, workerScenes, err)
	}
	if roster, err := m.Agents(); err != nil || len(roster) != 2 {
		t.Fatalf("Agents() = %+v err %v, want the file's two domains", roster, err)
	}

	// --- round 2 on the primary: a fresh tree, the same scene, the earlier round behind it ---
	second, err := primary.Search(memhop.SearchQuery{})
	if err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if second.Scene.SceneID != first.Scene.SceneID {
		t.Fatalf("the domain moved off its own conversation: %s -> %s", first.Scene.SceneID, second.Scene.SceneID)
	}
	if again, err := primary.PlanState(); err != nil || again.TotalCount != 0 {
		t.Fatalf("round two inherited round one's tree: %+v err %v", again, err)
	}
	ctx, err := primary.SceneContext("")
	if err != nil {
		t.Fatalf("SceneContext: %v", err)
	}
	if len(ctx.Topics) != 1 || ctx.Topics[0].TopicID != topic.ID {
		t.Fatalf("the conversation lists %+v, want round one alone: an open turn owns no topic record until it closes", ctx.Topics)
	}
	if _, err := primary.Update(memhop.TurnEnd{Input: "第二轮", Output: "又进一步", CreatedAt: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("second Update: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
