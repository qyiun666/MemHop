// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

// An index entry the record area does not back is damage, and it has to reach
// the caller with a code: the frame decoder answers that spot with io.EOF, a
// bare error whose code reads 0 — which is the success code — so the refusal
// would arrive wearing no verdict at all.
func TestReadRecordCodesAnIndexEntryTheLogDoesNotHold(t *testing.T) {
	eng, err := Create(tempPath(t, "index-past-log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if _, err := eng.WriteRecord(DefaultAgentID, RecL0Profile, 4242, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	// What an unchecked snapshot entry leaves behind: a live id pointing at the
	// end of the record area.
	eng.mu.Lock()
	pastLog := eng.nextOffset
	eng.index[DefaultAgentID][4242] = pastLog
	eng.mu.Unlock()

	_, _, err = eng.ReadRecord(DefaultAgentID, 4242)
	if err == nil {
		t.Fatal("an index entry past the log read back as a record")
	}
	if code := common.CodeOf(err); code != common.ErrCorruption {
		t.Fatalf("want the index/log disagreement coded as corruption, got code %d: %v", code, err)
	}
}
