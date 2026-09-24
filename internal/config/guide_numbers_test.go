// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// Three numbers in the host guide's knob table are refusals the code performs: the
// default retention window, the longest window the sweep can represent, and the longest
// timeout the client can hold. They are quoted in prose, so a change to the constant
// would leave the guide teaching a value `Open` now rejects — which is worse than
// silence for the two ceilings, since a host following the table writes a number it
// believes is accepted. Each phrase is matched to the constant behind it, in both
// languages.

package config

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

func TestGuideStatesTheNumbersTheCodeRefuses(t *testing.T) {
	cases := map[string][]struct {
		expr string
		want int64
		what string
	}{
		"../../INTEGRATION_GUIDE.md": {
			{`(\d+) \(7 days\)`, DefaultMemHopDefaults.ContentRetentionMs, "retention default"},
			{`can measure is (\d+) ms`, MaxContentRetentionMs, "retention ceiling"},
			{`past (\d+) s the seconds`, MaxTimeoutSecs, "timeout ceiling"},
		},
		"../../INTEGRATION_GUIDE.zh.md": {
			{`(\d+)（7 天）`, DefaultMemHopDefaults.ContentRetentionMs, "保留窗默认值"},
			{`最长窗口是 (\d+) ms`, MaxContentRetentionMs, "保留窗上限"},
			{`超过 (\d+) 秒`, MaxTimeoutSecs, "超时上限"},
		},
	}
	for path, claims := range cases {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(raw)
		for _, c := range claims {
			m := regexp.MustCompile(c.expr).FindStringSubmatch(text)
			if m == nil {
				t.Errorf("%s: no phrase matching %q to state the %s", path, c.expr, c.what)
				continue
			}
			got, err := strconv.ParseInt(m[1], 10, 64)
			if err != nil {
				t.Fatalf("%s: unparsable %s: %v", path, c.what, err)
			}
			if got != c.want {
				t.Errorf("%s: the guide states %d for the %s, the code refuses past %d",
					path, got, c.what, c.want)
			}
		}
	}
}
