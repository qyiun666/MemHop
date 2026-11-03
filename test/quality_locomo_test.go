// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build integration

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	memhop "github.com/qyiun666/MemHop/api"
	internal "github.com/qyiun666/MemHop/internal"
	"github.com/qyiun666/MemHop/test/testsupport"
)

type locomoAnswerVerdict struct {
	Correct bool   `json:"correct"`
	Reason  string `json:"reason"`
}

// TestLoCoMoQASmoke is the initial release smoke for model quality, not an
// accuracy estimate for the full dataset. Questions only read the transcripts
// recalled after ingestion; they never open or settle another memory turn.
func TestLoCoMoQASmoke(t *testing.T) {
	cfg := &internal.MemHopConfig{}
	if err := testsupport.LoadLLMConfig(cfg); err != nil {
		t.Skipf("LoCoMo QA LLM not configured: %v", err)
	}
	sample := loadLocomo10(t, 1)[0]
	if len(sample.QA) < 3 {
		t.Fatalf("sample %s has %d QA items, want at least 3", sample.SampleID, len(sample.QA))
	}

	db := testsupport.OpenMemHop(t)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close MemHop: %v", err)
		}
	})
	sceneIDs := ingestLocomoSample(t, db, sample)
	memory := recallLocomoMemory(t, db, sceneIDs)
	client := newJudge(t)
	questions := sample.QA[:3]
	t.Logf("locomo quality model=%s endpoint=%s sample_id=%s questions=%d",
		cfg.LLM.Model, cfg.LLM.APIURL, sample.SampleID, len(questions))

	correct := 0
	for _, qa := range questions {
		answer := answerLocomoQuestion(t, client, cfg.LLM.Model, memory, qa.Question)
		verdict := judgeLocomoAnswer(t, client, cfg.LLM.Model, qa, answer)
		if verdict.Correct {
			correct++
		}
		t.Logf("locomo qa sample_id=%s question=%q category=%d correct=%t correct_count=%d/%d answer=%q judge_reason=%s",
			sample.SampleID, qa.Question, qa.Category, verdict.Correct, correct, len(questions), answer, verdict.Reason)
	}
	t.Logf("locomo quality summary model=%s endpoint=%s sample_id=%s correct=%d/%d",
		cfg.LLM.Model, cfg.LLM.APIURL, sample.SampleID, correct, len(questions))
	if correct != len(questions) {
		t.Fatalf("LoCoMo initial release smoke: %d/%d judged correct, want all %d", correct, len(questions), len(questions))
	}
}

func ingestLocomoSample(t *testing.T, db *testsupport.Handle, sample locomoItem) []string {
	t.Helper()
	sceneIDs := make([]string, 0, len(sample.Sessions))
	stamp := time.Now().UnixMilli()
	for _, session := range sample.Sessions {
		res, err := db.Search(memhop.SearchQuery{NewScene: true})
		if err != nil {
			t.Fatalf("open scene %s: %v", session.ID, err)
		}
		sceneID := res.Scene.SceneID
		sceneIDs = append(sceneIDs, sceneID)
		title := session.ID
		if _, err := db.UpdateScene(sceneID, memhop.ScenePatch{Name: &title}); err != nil {
			t.Fatalf("title scene %s: %v", session.ID, err)
		}
		for i := 0; i+1 < len(session.Turns); i += 2 {
			if i > 0 {
				if _, _, err := db.OpenTurn(sceneID); err != nil {
					t.Fatalf("open turn %d in %s: %v", i/2, session.ID, err)
				}
			}
			stamp += 1000
			user := session.Turns[i].Speaker + ": " + session.Turns[i].Text
			agent := session.Turns[i+1].Speaker + ": " + session.Turns[i+1].Text
			if err := db.CloseTurn(user, agent, stamp); err != nil {
				t.Fatalf("settle turn %d in %s: %v", i/2, session.ID, err)
			}
		}
	}
	return sceneIDs
}

func recallLocomoMemory(t *testing.T, db *testsupport.Handle, sceneIDs []string) string {
	t.Helper()
	var memory strings.Builder
	for _, sceneID := range sceneIDs {
		scene, err := db.SceneContext(sceneID)
		if err != nil {
			t.Fatalf("recall scene %s: %v", sceneID, err)
		}
		fmt.Fprintf(&memory, "\n[%s]\n", scene.SceneName)
		for _, topic := range scene.Topics {
			for _, message := range topic.Messages {
				memory.WriteString(message.Content)
				memory.WriteByte('\n')
			}
		}
	}
	if memory.Len() == 0 {
		t.Fatal("read-only recall returned no memory")
	}
	return memory.String()
}

func answerLocomoQuestion(t *testing.T, client *openai.Client, model, memory, question string) string {
	t.Helper()
	prompt := fmt.Sprintf(`Answer the question using only the recalled memory below. Resolve relative dates from the date in each scene heading. Return only a concise answer, without explanation.

Question:
%s

Recalled memory:
%s`, question, memory)
	return qualityCompletion(t, client, model, prompt)
}

func judgeLocomoAnswer(t *testing.T, client *openai.Client, model string, qa locomoQA, answer string) locomoAnswerVerdict {
	t.Helper()
	prompt := fmt.Sprintf(`Judge whether the candidate answer is semantically correct for the question and reference answer. Equivalent wording and correct reasonable inferences count; missing or contradictory required facts do not. Answer strictly as JSON: {"correct": true|false, "reason": "one short sentence"}.

Question:
%s

Reference answer:
%s

Candidate answer:
%s`, qa.Question, qa.Answer, answer)
	raw := qualityCompletion(t, client, model, prompt)
	var verdict locomoAnswerVerdict
	if err := json.Unmarshal([]byte(stripJSONFence(raw)), &verdict); err != nil {
		t.Fatalf("parse LoCoMo judge verdict: %v (raw=%q)", err, raw)
	}
	return verdict
}

func qualityCompletion(t *testing.T, client *openai.Client, model, prompt string) string {
	t.Helper()
	resp, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:       model,
		Messages:    []openai.ChatCompletionMessage{{Role: "user", Content: prompt}},
		Temperature: 0,
	})
	if err != nil {
		t.Fatalf("LoCoMo quality completion: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("LoCoMo quality completion returned no choices")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content)
}
