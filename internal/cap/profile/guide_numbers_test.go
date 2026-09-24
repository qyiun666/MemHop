// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// The digest's row in the host guide quotes five numbers about this package's own
// behaviour. They are prose, so nothing stops them from outliving a change to the
// budgets they describe — and a host plans its prompt against exactly those numbers.
// Each claim is matched to the constant (or the render) that produces it, in its own
// phrase, in both languages.

package profile

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/qyiun666/MemHop/internal/repo/core"
)

type numberClaim struct {
	expr string
	want int
	what string
}

func TestGuideStatesTheBriefsOwnNumbers(t *testing.T) {
	// The line count is not a constant: render every track the digest knows and count
	// the lines the guide promises.
	full := core.ProfileSlot{
		Name: "guide", Role: "assistant", Personality: "curious",
		MBTI:         core.MBTIScore{IE: 0.4, NS: 0.4, TF: 0.4, JP: 0.4, Type: "ESTP"},
		Preferences:  map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5", "f": "6"},
		EmotionState: core.EmotionScore{Valence: 0.8, Arousal: 0.6, Dominance: 0.4},
	}
	lines := strings.Count(Brief(full), "\n")

	cases := map[string][]numberClaim{
		"../../../INTEGRATION_GUIDE.md": {
			{`at most (\d+) lines`, lines, "line count"},
			{`capped at (\d+) runes`, briefFieldMaxRunes, "field cap"},
			{`preference value at (\d+)`, briefValueMaxRunes, "preference-value cap"},
			{`the (\d+) lowest preference`, briefPreferencesShown, "preferences shown"},
			{`and (\d+) runes as the ceiling`, briefWorstCaseRunes, "worst-case ceiling"},
		},
		"../../../INTEGRATION_GUIDE.zh.md": {
			{`最多 (\d+) 行`, lines, "行数"},
			{`字段截到 (\d+) 字`, briefFieldMaxRunes, "字段上限"},
			{`值截到 (\d+) 字`, briefValueMaxRunes, "偏好值上限"},
			{`最低的 (\d+) 条`, briefPreferencesShown, "取几条偏好"},
			{`整段上界 (\d+) 字`, briefWorstCaseRunes, "整段上界"},
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
			got, err := strconv.Atoi(m[1])
			if err != nil {
				t.Fatalf("%s: unparsable %s: %v", path, c.what, err)
			}
			if got != c.want {
				t.Errorf("%s: the guide states %d for the %s, the code gives %d", path, got, c.what, c.want)
			}
		}
	}
}
