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

	memhop "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/sub"
	"github.com/qyiun666/MemHop/internal/sub/common"
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
	cfg := &sub.MemHopConfig{}
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

// fusedTopicKeywords flattens only FusedKeywords from compressed topics.
// The post-Dream Search also creates a fresh raw topic for the query itself;
// its UserKeywords describe the query, not the compressed memory.
func fusedTopicKeywords(ts *sub.SearchResult) []string {
	seen := map[string]struct{}{}
	var out []string
	for i := range ts.Contexts {
		for _, kw := range ts.Contexts[i].FusedKeywords {
			kw = strings.TrimSpace(kw)
			if kw == "" {
				continue
			}
			if _, ok := seen[kw]; ok {
				continue
			}
			seen[kw] = struct{}{}
			out = append(out, kw)
		}
	}
	return out
}

// topicKeywords flattens a TopicSlot's keyword tracks into one set for
// fidelity judgement (user + agent + fused).
func topicKeywords(ts *sub.SearchResult) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(kws []string) {
		for _, k := range kws {
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
	for i := range ts.Contexts {
		add(ts.Contexts[i].UserKeywords)
		add(ts.Contexts[i].AgentKeywords)
		add(ts.Contexts[i].FusedKeywords)
	}
	return out
}

// TestKeywordFidelity verifies point 1: the keywords MemHop extracts from a
// dialogue utterance faithfully carry that utterance's meaning.
func TestKeywordFidelity(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()
	judge := newJudge(t)
	model := judgeModel(t)

	cases := []string{
		"我喜欢在周末去海边跑步，尤其是清晨人少的时候",
		"我去年五月七号去参加了 LGBTQ 支持小组的活动",
		"我的猫叫咪咪，今年三岁，最喜欢吃三文鱼罐头",
	}
	base := time.Now().UnixMilli()
	var faithful, total int
	for i, text := range cases {
		res, err := db.Search(sub.SearchQuery{Text: text, AutoCreate: true, Timestamp: base + int64(i)*1000})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		kws := topicKeywords(res)
		if len(kws) == 0 {
			t.Fatalf("no keywords returned for %q", text)
		}
		v := judgeFaithful(t, judge, model, text, kws)
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

// TestKeywordPersistence verifies point 2: after several Search/Update cycles,
// the keyword context stored earlier is still retrievable.
func TestKeywordPersistence(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()

	base := time.Now().UnixMilli()
	// Store a distinctive fact early.
	anchor := "我的狗叫旺财，是一只金毛，今年五岁了"
	res, err := db.Search(sub.SearchQuery{Text: anchor, AutoCreate: true, Timestamp: base})
	if err != nil {
		t.Fatalf("anchor Search: %v", err)
	}
	anchorTopic := common.FormatHash(res.NewTopicID)

	// Noise: several unrelated Search/Update cycles.
	noise := []string{
		"今天天气真不错，适合出门散步",
		"我最近在学 Go 语言，觉得协程很有意思",
		"晚饭吃了红烧肉，有点油腻",
		"周末打算去看一场电影",
	}
	for i, ntext := range noise {
		ts := base + int64(i+1)*1000
		r, err := db.Search(sub.SearchQuery{Text: ntext, AutoCreate: true, Timestamp: ts})
		if err != nil {
			t.Fatalf("noise Search: %v", err)
		}
		if _, err := db.Update(common.FormatHash(r.NewTopicID), "好的，记下了", ts+500); err != nil {
			t.Fatalf("noise Update: %v", err)
		}
	}
	// Also update the anchor topic to simulate continued activity.
	if _, err := db.Update(anchorTopic, "旺财真可爱", base+100); err != nil {
		t.Fatalf("anchor Update: %v", err)
	}

	// Retrieve with a query about the anchor fact.
	got, err := db.Search(sub.SearchQuery{Text: "我的狗叫什么名字，多大了", Timestamp: base + 10000})
	if err != nil {
		t.Fatalf("retrieve Search: %v", err)
	}
	kws := topicKeywords(got)
	joined := strings.ToLower(strings.Join(kws, " "))
	if !strings.Contains(joined, "旺财") {
		t.Fatalf("anchor keyword 旺财 lost after noise cycles; got keywords=%v", kws)
	}
	t.Logf("persistence OK: anchor keywords still retrievable: %v", kws)
}

// ingestSameScene feeds a group of related turns into one scene (first turn
// AutoCreate, the rest directed), adding an agent Update after each user turn
// so Dream fused-topic IDs do not collide with original topics. Returns the scene ID.
func ingestSameScene(t *testing.T, db *memhop.DB, texts []string, base int64) uint64 {
	t.Helper()
	var sceneID uint64
	for i, text := range texts {
		ts := base + int64(i)*1000
		q := sub.SearchQuery{Text: text, Timestamp: ts}
		if i == 0 {
			q.AutoCreate = true
		} else {
			sid := common.FormatHash(sceneID)
			q.DirectedL2ID = &sid
		}
		res, err := db.Search(q)
		if err != nil {
			t.Fatalf("ingest Search[%d]: %v", i, err)
		}
		if i == 0 {
			if len(res.Contexts) == 0 {
				t.Fatal("first ingest returned no context")
			}
			sceneID = res.Contexts[0].SceneID
		}
		// Add the agent reply: every user turn has an agent response in real usage.
		if _, err := db.Update(common.FormatHash(res.NewTopicID), "好的，我记下了。", ts+500); err != nil {
			t.Fatalf("ingest Update: %v", err)
		}
	}
	return sceneID
}

// TestDreamCompressionFidelity verifies real compression: >20 related
// utterances in one scene are merged by Dream, and the fused topic's keywords
// must faithfully summarize all merged details.
func TestDreamCompressionFidelity(t *testing.T) {
	db := testsupport.OpenMemHop(t)
	defer db.Close()
	judge := newJudge(t)
	model := judgeModel(t)

	base := time.Now().UnixMilli()
	// 25 same-topic (running habit) turns exceed DreamCompressMinTopics=20 and trigger
	// real compression; overlapping semantics should make the LLM merge them.
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
	t.Logf("ingesting %d related utterances into one scene", len(related))
	ingestSameScene(t, db, related, base)

	compressed, err := db.Dream(context.Background(), "")
	if err != nil {
		t.Fatalf("Dream: %v", err)
	}
	t.Logf("Dream compressed=%v", compressed)

	// After Dream, retrieve on the theme and inspect the keywords now returned.
	res, err := db.Search(sub.SearchQuery{Text: "我的跑步习惯是怎样的", Timestamp: base + 60000})
	if err != nil {
		t.Fatalf("post-dream Search: %v", err)
	}
	kws := topicKeywords(res)
	if len(kws) == 0 {
		t.Fatal("no keywords after Dream")
	}
	// Post-compression retrieval includes the fused topic (non-empty FusedKeywords);
	// per the model, User/Agent tracks are empty when FusedKeywords carry values.
	var fusedSeen bool
	for i := range res.Contexts {
		c := &res.Contexts[i]
		if len(c.FusedKeywords) > 0 {
			fusedSeen = true
			if len(c.UserKeywords) > 0 || len(c.AgentKeywords) > 0 {
				t.Errorf("fused topic should have empty User/Agent keywords, got user=%v agent=%v",
					c.UserKeywords, c.AgentKeywords)
			}
		}
	}
	if !fusedSeen {
		t.Error("no fused topic in post-dream contexts")
	}

	// Judge whether the returned keywords faithfully summarize the running theme.
	source := strings.Join(related[:4], "；") // the core 4 turns hold all facts
	v := judgeFaithful(t, judge, model, source, kws)
	t.Logf("post-dream fidelity=%v kws=%v reason=%s", v.Faithful, kws, v.Reason)
	if !v.Faithful {
		t.Fatalf("keywords after Dream do not faithfully summarize compressed topics: %v", kws)
	}
}

// judgeModel returns the configured judge model name.
func judgeModel(t *testing.T) string {
	t.Helper()
	cfg := &sub.MemHopConfig{}
	if err := testsupport.LoadLLMConfig(cfg); err != nil {
		t.Skipf("judge LLM not configured: %v", err)
	}
	if cfg.LLM.Model == "" {
		return "deepseek-chat"
	}
	return cfg.LLM.Model
}

// ensure memhop import is used (OpenMemHop returns *memhop.DB).
var _ = memhop.Open
