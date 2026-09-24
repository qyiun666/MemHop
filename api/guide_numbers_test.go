// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The two guides quote numbers a host plans against: the tuning defaults, the per-record write
// budgets, the bound on the retention window. They are prose, so nothing stops one of them from
// outliving the value it describes — and the host finds out by building the wrong client, not by a
// failing test. Each claim is matched to what actually produces the number, in its own phrase, in
// both languages: a reworded sentence fails the claim rather than passing silently. The transport's own
// defaults are paired with their constants over there (`internal/llm/guide_numbers_test.go`), since those
// numbers exist only as what the library answers when a field is left alone.

package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// numberClaim is one quoted number and the value it has to agree with.
type numberClaim struct {
	pattern string
	want    int64
	what    string
}

func TestGuideNumbersMatchTheSurface(t *testing.T) {
	defaults := DefaultMemHopDefaults
	cases := map[string][]numberClaim{
		"../INTEGRATION_GUIDE.md": {
			{`\| SceneDreamTopicThreshold \| (\d+) \|`, int64(defaults.SceneDreamTopicThreshold), "dream trigger"},
			{`\| DreamCompressMinTopics \| (\d+) \|`, int64(defaults.DreamCompressMinTopics), "compression floor"},
			{`\| AgentIdleTTLMs \| (\d+) \|`, defaults.AgentIdleTTLMs, "idle TTL"},
			{`\| ContentRetentionMs \| (\d+)`, defaults.ContentRetentionMs, "retention window"},
			{"MaxEventPayloadBytes`?\\s*\\((\\d+) KiB\\)", MaxEventPayloadBytes / 1024, "event budget (KiB)"},
			{"MaxUtterancePayloadBytes`?\\s*\\((\\d+) KiB\\)", MaxUtterancePayloadBytes / 1024, "utterance budget (KiB)"},
			{"MaxSubAgentNameBytes`? ?\\((\\d+)\\)", MaxSubAgentNameBytes, "tenant-key cap"},
		},
		"../INTEGRATION_GUIDE.zh.md": {
			{`\| SceneDreamTopicThreshold \| (\d+) \|`, int64(defaults.SceneDreamTopicThreshold), "触发阈值"},
			{`\| DreamCompressMinTopics \| (\d+) \|`, int64(defaults.DreamCompressMinTopics), "合并下限"},
			{`\| AgentIdleTTLMs \| (\d+) \|`, defaults.AgentIdleTTLMs, "空闲回收"},
			{`\| ContentRetentionMs \| (\d+)`, defaults.ContentRetentionMs, "保留窗"},
			{"MaxEventPayloadBytes`（(\\d+) KiB", MaxEventPayloadBytes / 1024, "事件预算（KiB）"},
			{"MaxUtterancePayloadBytes`（(\\d+) KiB", MaxUtterancePayloadBytes / 1024, "原文预算（KiB）"},
			{"MaxSubAgentNameBytes`（(\\d+)）", MaxSubAgentNameBytes, "租户键上界"},
		},
	}
	for path, claims := range cases {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(raw)
		for _, claim := range claims {
			re, err := regexp.Compile(claim.pattern)
			if err != nil {
				t.Fatalf("%s: %s does not compile: %v", path, claim.pattern, err)
			}
			found := re.FindStringSubmatch(text)
			if found == nil {
				t.Errorf("%s: the phrase for %s is not in the guide any more (%s)", path, claim.what, claim.pattern)
				continue
			}
			got, err := strconv.ParseInt(found[1], 10, 64)
			if err != nil {
				t.Fatalf("%s: %s: %q is not a number", path, claim.what, found[1])
			}
			if got != claim.want {
				t.Errorf("%s: the guide says %s = %d, the surface says %d", path, claim.what, got, claim.want)
			}
		}
	}
}

// The ceiling is the one number in that table the host cannot read as a constant — and the thing a
// host does with it is either write a bigger window or not. So the claim is checked as behaviour:
// the largest window the guide promises is representable and one millisecond past it is refused
// before the file is touched.
func TestGuideRetentionCeilingIsTheRefusalBoundary(t *testing.T) {
	const ceiling = 9223372036854
	for _, path := range []string{"../INTEGRATION_GUIDE.md", "../INTEGRATION_GUIDE.zh.md"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(raw), strconv.FormatInt(ceiling, 10)) {
			t.Errorf("%s no longer states the ceiling %d ms the surface refuses past", path, ceiling)
		}
	}
	dir := t.TempDir()
	at := func(ms int64) error {
		d := DefaultMemHopDefaults
		d.ContentRetentionMs = ms
		db, err := Open(filepath.Join(dir, "ceiling.meh"), surfaceLLM("http://127.0.0.1:1"), d, surfaceProfile())
		if err == nil {
			_ = db.Close()
		}
		return err
	}
	if err := at(ceiling); err != nil {
		t.Fatalf("the advertised longest window was refused: %v", err)
	}
	if err := at(ceiling + 1); CodeOf(err) != ErrConfig {
		t.Fatalf("a window past the ceiling answered %v (code %d), want ErrConfig", err, CodeOf(err))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the refused window left %d file(s) behind on a path the host named", len(entries))
	}
}
