// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The two LlmConfig budgets have no public constants — they are what the library answers when a
// host leaves the field alone — and the guides state them in prose. This ties each sentence to the
// constant behind it, in both languages, so a change here has to be made in the same breath as the
// wording a host reads.

package llm

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

func TestGuideQuotesTheTransportDefaults(t *testing.T) {
	cases := map[string][]struct {
		pattern string
		want    int
		what    string
	}{
		"../../INTEGRATION_GUIDE.md": {
			{`unfilled answers (\d+)\. A zero is never`, defaultTimeoutSecs, "timeout default"},
			{`unfilled answers (\d+)\. The prompt budgets`, defaultMaxOutputTokens, "output ceiling default"},
		},
		"../../INTEGRATION_GUIDE.zh.md": {
			{`没填由库答 (\d+)。0 永远不会`, defaultTimeoutSecs, "超时默认"},
			{`没填由库答 (\d+)。提示词`, defaultMaxOutputTokens, "输出上限默认"},
		},
	}
	for path, claims := range cases {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, claim := range claims {
			found := regexp.MustCompile(claim.pattern).FindStringSubmatch(string(raw))
			if found == nil {
				t.Errorf("%s: the phrase for %s is gone (%s)", path, claim.what, claim.pattern)
				continue
			}
			got, err := strconv.Atoi(found[1])
			if err != nil {
				t.Fatalf("%s: %s: %q is not a number", path, claim.what, found[1])
			}
			if got != claim.want {
				t.Errorf("%s: the guide says %s = %d, the transport answers %d", path, claim.what, got, claim.want)
			}
		}
	}
}
