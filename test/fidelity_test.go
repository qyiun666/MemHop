// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build integration

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// judgeVerdict is the LLM judge's structured decision on keyword fidelity.
type judgeVerdict struct {
	Faithful bool   `json:"faithful"`
	Reason   string `json:"reason"`
}

// newJudge builds an LLM client for fidelity judgement, reusing the test LLM
// config. Returns nil (and skips) when no key is configured.
func newJudge(t *testing.T) *openai.Client {
	t.Helper()
	cfg := &internal.MemHopConfig{}
	if err := testsupport.LoadLLMConfig(cfg); err != nil {
		t.Skipf("judge LLM not configured: %v", err)
	}
	ocfg := openai.DefaultConfig(cfg.LLM.APIKey)
	ocfg.BaseURL = strings.TrimSuffix(cfg.LLM.APIURL, "/chat/completions")
	ocfg.HTTPClient = &http.Client{Timeout: 120 * time.Second}
	return openai.NewClientWithConfig(ocfg)
}

// judgeFaithful asks the LLM whether the given keywords faithfully capture the
// meaning of the source text — i.e. whether someone seeing only the keywords
// could infer the core fact of the source.
func judgeFaithful(t *testing.T, cli *openai.Client, model, sourceText string, keywords []string) judgeVerdict {
	t.Helper()
	kw := strings.Join(keywords, ", ")
	prompt := fmt.Sprintf(`You are evaluating whether a set of keywords faithfully represents a source utterance.

Source utterance:
%s

Extracted keywords:
%s

Question: Do these keywords capture the core meaning/facts of the source utterance, such that someone seeing ONLY the keywords could infer what the source was about? Answer strictly as JSON: {"faithful": true|false, "reason": "one short sentence"}.`, sourceText, kw)

	resp, err := cli.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{Role: "user", Content: prompt},
		},
		Temperature: 0,
	})
	if err != nil {
		t.Fatalf("judge completion: %v", err)
	}
	var v judgeVerdict
	if err := json.Unmarshal([]byte(stripJSONFence(resp.Choices[0].Message.Content)), &v); err != nil {
		t.Fatalf("judge verdict parse: %v (raw=%q)", err, resp.Choices[0].Message.Content)
	}
	return v
}

// stripJSONFence removes ```json ... ``` fences the LLM may wrap output in.
func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// surfaceKeywords flattens a session surface into one deduplicated keyword set:
// every depth-1 topic's single keyword track, in turn order.
func surfaceKeywords(res *memhop.SearchResult) []string {
	seen := map[string]struct{}{}
	var out []string
	for i := range res.Topics {
		for _, k := range res.Topics[i].FusedKeywords {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	return out
}

// TestKeywordFidelity verifies point 1: the keywords Update distills from a
// finished turn faithfully carry that turn's meaning — the keywords ARE the
// host's context, so this is the quality bar of the whole design.
func TestKeywordFidelity(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()
	judge := newJudge(t)
	model := judgeModel(t)

	cases := [][2]string{
		{"我喜欢在周末去海边跑步，尤其是清晨人少的时候", "海边清晨人少时跑步最舒服，注意防晒"},
		{"我去年五月七号去参加了 LGBTQ 支持小组的活动", "那是个支持性很强的场合，继续聊？"},
		{"我的猫叫咪咪，今年三岁，最喜欢吃三文鱼罐头", "咪咪的口味很明确，三文鱼罐头是它的最爱"},
	}
	sceneID, err := db.OpenSession("keyword fidelity")
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	base := time.Now().UnixMilli()
	var faithful, total int
	for i, c := range cases {
		ts := base + int64(i)*1000
		topicID, err := db.Update(memhop.TurnUpdate{
			SceneID: sceneID, UserText: c[0], UserTS: ts, AgentText: c[1], AgentTS: ts + 500,
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		var kws []string
		for _, tp := range res.Topics {
			if tp.ID == topicID {
				kws = tp.FusedKeywords
			}
		}
		if len(kws) == 0 {
			t.Fatalf("no keywords distilled for turn %d", i)
		}
		v := judgeFaithful(t, judge, model, c[0]+" / "+c[1], kws)
		total++
		if v.Faithful {
			faithful++
		}
		t.Logf("case[%d] faithful=%v kws=%v reason=%s", i, v.Faithful, kws, v.Reason)
	}
	t.Logf("keyword fidelity: %d/%d faithful", faithful, total)
	if faithful == 0 {
		t.Fatalf("keywords failed to capture source meaning for all %d cases", total)
	}
}

// TestKeywordPersistence verifies point 2: within one host session, an anchor
// fact's keywords are still on the surface after unrelated turns pile on top
// of it (the surface is per-turn, so nothing overwrites the anchor).
func TestKeywordPersistence(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()

	sceneID, err := db.OpenSession("persistence")
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	base := time.Now().UnixMilli()
	if _, err := db.Update(memhop.TurnUpdate{
		SceneID: sceneID, UserText: "我的狗叫旺财，是一只金毛，今年五岁了", UserTS: base,
		AgentText: "旺财这个名字很顺口", AgentTS: base + 500,
	}); err != nil {
		t.Fatalf("anchor Update: %v", err)
	}

	noise := []string{
		"今天天气真不错，适合出门散步",
		"我最近在学 Go 语言，觉得协程很有意思",
		"晚饭吃了红烧肉，有点油腻",
		"周末打算去看一场电影",
	}
	for i, ntext := range noise {
		ts := base + int64(i+1)*1000
		if _, err := db.Update(memhop.TurnUpdate{
			SceneID: sceneID, UserText: ntext, UserTS: ts,
			AgentText: "好的，记下了", AgentTS: ts + 500,
		}); err != nil {
			t.Fatalf("noise Update: %v", err)
		}
	}

	res, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("session read: %v", err)
	}
	if len(res.Topics) != 5 {
		t.Fatalf("surface = %d topics, want all 5 turns kept", len(res.Topics))
	}
	kws := surfaceKeywords(res)
	joined := strings.ToLower(strings.Join(kws, " "))
	if !strings.Contains(joined, "旺财") {
		t.Fatalf("anchor keyword 旺财 lost after noise turns; keywords=%v", kws)
	}
	t.Logf("persistence OK: anchor keyword survived 4 noise turns: %v", kws)
}

// ingestSession feeds a group of related turns into one host session, each
// closed with an agent reply. Returns the session id.
func ingestSession(t *testing.T, db *testsupport.Handle, texts []string, base int64) string {
	t.Helper()
	sceneID, err := db.OpenSession("ingest session")
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	for i, text := range texts {
		ts := base + int64(i)*1000
		if _, err := db.Update(memhop.TurnUpdate{
			SceneID: sceneID, UserText: text, UserTS: ts,
			AgentText: "好的，我记下了。", AgentTS: ts + 500,
		}); err != nil {
			t.Fatalf("ingest Update[%d]: %v", i, err)
		}
	}
	return sceneID
}

// TestDreamCompressionFidelity verifies real consolidation: >20 related turns
// in one session are merged by Dream, the surface shrinks, and the fused
// topic's keywords still faithfully summarize the merged details.
func TestDreamCompressionFidelity(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()
	judge := newJudge(t)
	model := judgeModel(t)

	base := time.Now().UnixMilli()
	// 25 same-topic (running habit) turns exceed DreamCompressMinTopics=20 and
	// trigger real compression; overlapping semantics make the LLM merge them.
	related := []string{
		"我喜欢早上六点去公园慢跑",
		"跑步的时候我习惯听播客",
		"我每周跑步大概三次，每次五公里",
		"跑完步我会喝一杯蛋白粉",
		"我早上六点出门跑步",
		"慢跑时我听健身播客",
		"我一周跑三次步",
		"每次跑步五公里左右",
		"运动后我喝蛋白粉补充",
		"清晨六点我在公园跑步",
		"跑步路上我放播客听",
		"每周三次是我的跑步频率",
		"五公里是我每次的跑步距离",
		"跑完我习惯喝杯蛋白粉",
		"我早上喜欢去公园慢跑",
		"边跑步边听播客是我的习惯",
		"我每周坚持跑三次",
		"每次我都跑五公里",
		"跑步后我必喝蛋白粉",
		"六点起床去公园跑步",
		"慢跑配播客最舒服",
		"一周三次跑步不间断",
		"五公里跑完很爽",
		"蛋白粉是跑后必备",
		"早上公园跑步我很享受",
	}
	t.Logf("ingesting %d related utterances into one session", len(related))
	sceneID := ingestSession(t, db, related, base)

	before, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("session read before dream: %v", err)
	}
	rep, err := db.Dream(context.Background(), sceneID)
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	t.Logf("Dream consolidated=%d compressed_groups=%d stages=%d", rep.ConsolidatedScenes, rep.L2TopicsCompressed, len(rep.Stages))

	after, err := db.Search(memhop.SearchQuery{SceneID: sceneID})
	if err != nil {
		t.Fatalf("session read after dream: %v", err)
	}
	kws := surfaceKeywords(after)
	if len(kws) == 0 {
		t.Fatal("no keywords on the surface after Dream")
	}
	if rep.ConsolidatedScenes > 0 {
		if len(after.Topics) >= len(before.Topics) {
			t.Errorf("surface did not shrink: %d -> %d", len(before.Topics), len(after.Topics))
		}
		// The fused topic owns the merged turns and carries the single track.
		var fusedSeen bool
		for _, tp := range after.Topics {
			if len(tp.ChildrenIDs) > 0 && len(tp.FusedKeywords) > 0 {
				fusedSeen = true
			}
		}
		if !fusedSeen {
			t.Error("no fused topic (with children and keywords) on the post-dream surface")
		}
	}

	// Judge whether the surface keywords still carry the running theme.
	source := strings.Join(related[:4], "；") // the core 4 turns hold all facts
	v := judgeFaithful(t, judge, model, source, kws)
	t.Logf("post-dream fidelity=%v surface=%d topics kws=%v reason=%s", v.Faithful, len(after.Topics), kws, v.Reason)
	if !v.Faithful {
		t.Fatalf("keywords after Dream do not faithfully summarize compressed turns: %v", kws)
	}
}

// judgeModel returns the configured judge model name.
func judgeModel(t *testing.T) string {
	t.Helper()
	cfg := &internal.MemHopConfig{}
	if err := testsupport.LoadLLMConfig(cfg); err != nil {
		t.Skipf("judge LLM not configured: %v", err)
	}
	if cfg.LLM.Model == "" {
		return "deepseek-chat"
	}
	return cfg.LLM.Model
}
