// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package timeutil

import "time"

// NowMs returns the current Unix time in milliseconds.
func NowMs() int64 {
	return time.Now().UnixMilli()
}

// FromMs converts a millisecond Unix timestamp to time.Time.
func FromMs(ms int64) time.Time {
	return time.UnixMilli(ms)
}
