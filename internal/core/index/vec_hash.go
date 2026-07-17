// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package index

import (
	"fmt"

	"memhop/internal/hash"
)

// VecRecordHash derives the storage ID hash for a topic's centroid vector record.
func VecRecordHash(topicIDHash uint64) uint64 {
	return hash.HashID(fmt.Sprintf("v:%d", topicIDHash))
}
