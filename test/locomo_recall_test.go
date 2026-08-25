// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build integration

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/internal/common"
	"github.com/qyiun666/MemHop/test/testsupport"
)

// locomo10Item mirrors one conversation item (sessions + QA) in locomo10.json.
type locomo10Item struct {
	SampleID string `json:"sample_id"`
	SpeakerA string `json:"speaker_a"`
	SpeakerB string `json:"speaker_b"`
	Sessions []struct {
		ID    string `json:"id"`
		Turns []struct {
			Text    string `json:"text"`
			Speaker string `json:"speaker"`
		} `json:"turns"`
	} `json:"sessions"`
	QA []struct {
		Question string `json:"question"`
		Answer   string `json:"answer"`
		Category int    `json:"category"`
	} `json:"qa"`
}

// locomoRecallVerdict is the LLM judge's decision on whether the retrieved
// context can answer the question.
type locomoRecallVerdict struct {
	Answerable bool   `json:"answerable"`
	Reason     string `json:"reason"`
}

// BenchmarkLocomoRecall measures retrieval recall over the locomo10 fixture:
// ingest N items (MEMHOP_LOCOMO_ITEMS, default 1), run each QA pair through
// Search, and report LLM-judged recall + entity_hit.
func BenchmarkLocomoRecall(b *testing.B) {
	maxItems := 1
	if v := os.Getenv("MEMHOP_LOCOMO_ITEMS"); v != "" {
		fmt.Sscanf(v, "%d", &maxItems)
	}

	items := loadLocomo10(b, maxItems)
	db := testsupport.OpenMemHopB(b)
	defer db.Close()
	judge, model := newLocomoJudge(b)

	// Ingest all turns via the normal retrieval route (no AutoCreate, no
	// DirectedL2ID) so Search clusters into the best-scoring scene, matching how
	// a real host records user utterances; agent replies Update the Search topic.
	base := time.Now().UnixMilli()
	var seq int64
	for _, item := range items {
		for _, sess := range item.Sessions {
			sessionBase := locomoSessionBaseTS(sess.ID)
			if sessionBase == 0 {
				seq++
				sessionBase = base + seq*1000 // fallback: synthetic timeline
			}
			var activeTopic string
			for i, tn := range sess.Turns {
				seq++
				ts := sessionBase + int64(i)*30_000
				if tn.Speaker == item.SpeakerA {
					res, err := db.Search(context.Background(), internal.SearchQuery{Text: tn.Text, Timestamp: ts})
					if err != nil {
						b.Fatalf("ingest Search: %v", err)
					}
					activeTopic = common.FormatHash(res.NewTopicID)
				} else if activeTopic != "" {
					// Agent turn: record the reply on the user turn's topic.
					if err := db.Update(activeTopic, tn.Text, ts); err != nil {
						b.Fatalf("ingest Update: %v", err)
					}
				}
			}
		}
	}
	b.Logf("ingested %d items, %d turns", len(items), seq)

	// Measure recall over all QA pairs. A Search whose keyword extraction
	// returns malformed JSON is counted and skipped (like judge errors), so an
	// occasional LLM hiccup does not abort the whole benchmark.
	var hits, total, emptyCtx, judgeErr, searchErr int
	var entSum float64
	b.ResetTimer()
	for _, item := range items {
		for _, qa := range item.QA {
			seq++
			ts := base + seq*1000
			res, err := db.Search(context.Background(), internal.SearchQuery{Text: qa.Question, Timestamp: ts})
			if err != nil {
				searchErr++
				if searchErr <= 3 {
					b.Logf("recall Search error on %q: %v", qa.Question, err)
				}
				continue
			}
			total++
			ctxText := gatherLocomoContext(db, res)
			entSum += locomoEntityHit(qa.Answer, ctxText)
			if strings.TrimSpace(ctxText) == "" {
				emptyCtx++
				continue
			}
			verdict, jerr := locomoJudgeAnswerable(judge, model, qa.Question, qa.Answer, ctxText)
			if jerr != nil {
				judgeErr++
				if judgeErr <= 3 {
					b.Logf("judge error on %q: %v", qa.Question, jerr)
				}
				continue
			}
			if verdict {
				hits++
			}
		}
	}
	b.StopTimer()
	b.Logf("recall detail: hits=%d total=%d searchErr=%d emptyCtx=%d judgeErr=%d entity_hit=%.3f",
		hits, total, searchErr, emptyCtx, judgeErr, entSum/float64(total))
	if total > 0 {
		b.ReportMetric(float64(hits)/float64(total), "recall")
		b.ReportMetric(entSum/float64(total), "entity_hit")
		b.Logf("recall: %d/%d = %.3f", hits, total, float64(hits)/float64(total))
	}
}

// loadLocomo10 reads locomo10.json and returns up to maxItems conversation items.
func loadLocomo10(tb testing.TB, maxItems int) []locomo10Item {
	tb.Helper()
	path := filepath.Join("..", "benches", "fixtures", "locomo10.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("read fixture: %v", err)
	}
	var fx struct {
		Items []locomo10Item `json:"items"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		tb.Fatalf("parse fixture: %v", err)
	}
	if len(fx.Items) == 0 {
		tb.Fatal("fixture has no items")
	}
	if maxItems > 0 && maxItems < len(fx.Items) {
		fx.Items = fx.Items[:maxItems]
	}
	return fx.Items
}

// gatherLocomoContext collects the keyword context from a search result: each
// returned topic's timestamps, User/Agent/Fused keywords plus the L4 archive
// text they reference. This is the context handed to the judge.
func gatherLocomoContext(db *memhop.DB, res *internal.SearchResult) string {
	var sb strings.Builder
	for i := range res.Contexts {
		c := &res.Contexts[i]
		if c.UserTimestamp > 0 {
			fmt.Fprintf(&sb, "[user: %s] ", time.UnixMilli(c.UserTimestamp).UTC().Format("2006-01-02 15:04"))
		}
		if c.AgentTimestamp > 0 {
			fmt.Fprintf(&sb, "[agent: %s] ", time.UnixMilli(c.AgentTimestamp).UTC().Format("2006-01-02 15:04"))
		}
		sb.WriteByte('\n')
		for _, kw := range c.UserKeywords {
			sb.WriteString(kw)
			sb.WriteByte(' ')
		}
		for _, kw := range c.AgentKeywords {
			sb.WriteString(kw)
			sb.WriteByte(' ')
		}
		for _, kw := range c.FusedKeywords {
			sb.WriteString(kw)
			sb.WriteByte(' ')
		}
		for _, ref := range c.L4Refs {
			if slot, err := db.GetArchive(common.FormatHash(ref)); err == nil {
				sb.WriteString(slot.Content)
				sb.WriteByte('\n')
			}
		}
	}
	return sb.String()
}

// locomoSessionBaseTS extracts the time embedded in a session id
// ("session_1_1:56 pm on 8 May, 2023") as Unix ms (UTC), or 0 when unparseable.
// Consumers must format with .UTC() so late-evening sessions do not shift days.
func locomoSessionBaseTS(id string) int64 {
	re := regexp.MustCompile(`(\d{1,2}:\d{2}(?::\d{2})?\s*(?:am|pm))\s+on\s+(\d{1,2}\s+\w+,?\s+\d{4})`)
	m := re.FindStringSubmatch(id)
	if m == nil {
		return 0
	}
	t, err := time.Parse("3:04 pm on 2 January, 2006", m[1]+" on "+m[2])
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// locomoEntityHit returns the fraction of answer tokens (numbers + words of
// length >= 3) present in the retrieved context; a judge-independent signal
// that short digit tokens can inflate, so treat it as diagnostic only.
func locomoEntityHit(answer, ctxText string) float64 {
	re := regexp.MustCompile(`\d+|[A-Za-z]{3,}`)
	ans := re.FindAllString(answer, -1)
	if len(ans) == 0 {
		return 0
	}
	lower := strings.ToLower(ctxText)
	hit := 0
	for _, tok := range ans {
		if strings.Contains(lower, strings.ToLower(tok)) {
			hit++
		}
	}
	return float64(hit) / float64(len(ans))
}

// newLocomoJudge builds an LLM client for recall judgement.
func newLocomoJudge(tb testing.TB) (*openai.Client, string) {
	tb.Helper()
	cfg := &internal.MemHopConfig{}
	if err := testsupport.LoadLLMConfig(cfg); err != nil {
		tb.Skipf("judge LLM not configured: %v", err)
	}
	conf := openai.DefaultConfig(cfg.LLM.APIKey)
	conf.BaseURL = strings.TrimSuffix(cfg.LLM.APIURL, "/chat/completions") // key_config.json stores the full endpoint
	conf.HTTPClient = &http.Client{Timeout: 120 * time.Second}
	model := cfg.LLM.Model
	if model == "" {
		model = "deepseek-chat"
	}
	return openai.NewClientWithConfig(conf), model
}

// locomoJudgeAnswerable asks the judge whether the retrieved context can answer
// the question, given the reference answer.
func locomoJudgeAnswerable(client *openai.Client, model, question, answer, ctxText string) (bool, error) {
	prompt := fmt.Sprintf(`You are evaluating a memory retrieval system.

Question: %s
Reference answer: %s

Retrieved context:
%s

Timestamps like [user: 2023-05-08 13:56] and [agent: ...] mark when each utterance happened; relative time references (e.g. "yesterday", "last week") must be resolved against them.

Can the retrieved context alone answer the question correctly (matching the reference answer's key fact)? Reply with ONLY valid JSON: {"answerable": true/false, "reason": "brief"}`, question, answer, ctxText)

	resp, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		Temperature: 0,
	})
	if err != nil || len(resp.Choices) == 0 {
		return false, err
	}
	var v locomoRecallVerdict
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &v); err != nil {
		return false, err
	}
	return v.Answerable, nil
}
