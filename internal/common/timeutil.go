// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package common

// ElapsedHours returns the whole/partial hours between two Unix-millisecond
// stamps, clamped at zero for clock skew (updatedAt in the future).
func ElapsedHours(nowMs, updatedAtMs int64) float64 {
	dtMs := max(nowMs-updatedAtMs, 0)
	return float64(dtMs) / 3_600_000.0
}
