// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// A file can hand back its index two ways: the checkpoint's snapshot names each record's
// frame directly, and a full scan derives the index by replaying the log — where a later
// frame for an id wins and a tombstone takes the id out. Those are two implementations of
// one judgement, and only one of them is chosen by an accident of history: whether the
// snapshot loads, or rotted, or was trimmed by the next delete.
//
// So the same bytes must answer identically either way. The fixture carries the three shapes
// where the two could disagree — an id whose newest frame is behind the snapshot, a record
// deleted before the checkpoint, and a second domain — and the digest reads payloads, not
// just ids, because pointing an id at the frame it was overwritten from is exactly the
// mistake that stays invisible in a set of keys.
func TestSnapshotAndFullScanAnswerIdentically(t *testing.T) {
	p := tempPath(t, "two_arms")
	eng, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	const sub = uint64(0x22)
	for _, rec := range []struct {
		agent uint64
		rt    uint8
		id    uint64
		data  string
	}{
		{DefaultAgentID, RecL0Profile, 1, "primary-v1"},
		{DefaultAgentID, RecL2Topic, 10, "swallowed"},
		{DefaultAgentID, RecL1SceneNode, 20, "node"},
		{DefaultAgentID, RecL5PlanNode, 30, "step"},
		{sub, RecL0Profile, 1, "sub-v1"},
		{sub, RecL2Topic, 11, "sub topic"},
	} {
		if _, err := eng.WriteRecord(rec.agent, rec.rt, rec.id, []byte(rec.data)); err != nil {
			t.Fatalf("write %x/%d: %v", rec.agent, rec.id, err)
		}
	}
	if _, err := eng.WriteRecord(DefaultAgentID, RecL0Profile, 1, []byte("primary-v2")); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if _, err := eng.DeleteRecord(DefaultAgentID, 10); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := eng.closeNoCheckpoint(); err != nil {
		t.Fatal(err)
	}

	// Arm one: the snapshot loads, and only the frames appended after it are scanned.
	fromSnapshot, snapOffset := digestOf(t, p, true)
	// Arm two: that same snapshot is made unreadable, so the whole log is replayed.
	flipByteAt(t, p, snapOffset+8)
	fromScan, _ := digestOf(t, p, false)

	if fromSnapshot != fromScan {
		t.Fatalf("the same file answers two ways depending on whether its snapshot loads:\n"+
			" snapshot arm: %s\n scan arm:     %s", fromSnapshot, fromScan)
	}
	// A comparison of two identical empty digests would pass, and this file is not empty: the
	// overwritten id, the deleted id and the second domain all have to show up in the answer.
	if n := strings.Count(fromSnapshot, ";"); n != 5 {
		t.Fatalf("the digest covers %d records, want exactly the five this fixture leaves alive: %s", n, fromSnapshot)
	}
	if strings.Contains(fromSnapshot, "swallowed") {
		t.Fatalf("the deleted record is in the answer either way: %s", fromSnapshot)
	}
	if !strings.Contains(fromSnapshot, "primary-v2") || strings.Contains(fromSnapshot, "primary-v1") {
		t.Fatalf("the overwritten id does not read from its newest frame in both arms: %s", fromSnapshot)
	}
}

// digestOf opens the file and renders every record the index names — its agent, type, id and
// payload — as one comparable string. wantSnapshot says which route the Open has to take, and
// a route that silently changes is not a weaker test but a different one, so this refuses it.
func digestOf(t *testing.T, path string, wantSnapshot bool) (string, uint64) {
	t.Helper()
	eng, err := Open(path)
	if err != nil {
		t.Fatalf("open (snapshot arm: %t): %v", wantSnapshot, err)
	}
	defer func() {
		if err := eng.closeNoCheckpoint(); err != nil {
			t.Fatal(err)
		}
	}()
	snap := eng.activeHeaderRef().SnapshotOffset
	if wantSnapshot && snap == 0 {
		t.Fatal("the snapshot was not consumed: this arm is a full scan, so the comparison below proves nothing")
	}
	if !wantSnapshot && snap != 0 {
		t.Fatal("the snapshot still loads: this arm is not the full scan it claims to be")
	}
	var parts []string
	for agent := range eng.IterAgents() {
		for _, rt := range []uint8{RecL0Profile, RecL1SceneNode, RecL2Topic, RecL5PlanNode} {
			ids := slices.Collect(eng.IndexByType(agent, rt))
			slices.Sort(ids)
			for _, id := range ids {
				storedRT, data, err := eng.ReadRecord(agent, id)
				if err != nil {
					t.Fatalf("read %x/%d: %v", agent, id, err)
				}
				if storedRT != rt {
					t.Fatalf("the type index names %x/%d as %d and the frame says %d", agent, id, rt, storedRT)
				}
				parts = append(parts, fmt.Sprintf("%d:%d:%s;", rt, id, data))
			}
		}
	}
	return strings.Join(parts, "|"), snap
}
