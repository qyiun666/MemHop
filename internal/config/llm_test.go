// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package config

import (
	"math"
	"testing"
	"time"
)

func completeEndpoint() LlmConfig {
	return LlmConfig{APIURL: "http://127.0.0.1:1/v1", APIKey: "k", Model: "m"}
}

// A timeout the HTTP client cannot hold is worse than no timeout at all: net/http only arms
// a deadline for a positive Client.Timeout, so seconds that wrapped around leave the call
// free to hang — and every LLM call runs inside its domain's lock, so a hung endpoint blocks
// that agent indefinitely. The ceiling is where that scaling stops fitting, and the value one
// past it is the hazard itself rather than a slightly longer wait.
func TestValidateRefusesUnrepresentableTimeouts(t *testing.T) {
	// Only a build whose int is wide enough can express the bad value: a 32-bit int tops out
	// near 68 years, comfortably inside the ceiling, so the rule there is that nothing fits.
	if int64(math.MaxInt) > MaxTimeoutSecs {
		for _, secs := range []int64{MaxTimeoutSecs + 1, math.MaxInt64} {
			llm := completeEndpoint()
			llm.TimeoutSecs = int(secs)
			if err := llm.Validate(); err == nil {
				t.Fatalf("timeout_secs %d was accepted", secs)
			}
		}
	}
	for _, secs := range []int64{0, -1, 30, MaxTimeoutSecs} {
		llm := completeEndpoint()
		llm.TimeoutSecs = int(secs)
		if err := llm.Validate(); err != nil {
			t.Fatalf("timeout_secs %d refused: %v", secs, err)
		}
	}

	if fits := time.Duration(MaxTimeoutSecs) * time.Second; fits <= 0 {
		t.Fatalf("the accepted ceiling no longer scales to a positive duration: %s", fits)
	}
	// One past the ceiling is the hazard, not a longer wait: the seconds scale past
	// MaxInt64 nanoseconds and come back negative, which is what reads as "no timeout".
	past := MaxTimeoutSecs + 1
	if wraps := time.Duration(past) * time.Second; wraps > 0 {
		t.Fatalf("one past the ceiling still scales positive (%s): the ceiling is no longer the overflow boundary", wraps)
	}
}
