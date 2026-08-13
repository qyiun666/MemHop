// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package repo

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/sub/common"
	"github.com/qyiun666/MemHop/internal/sub/repo/core"
)

func TestAppendReadTrajectoryOrdered(t *testing.T) {
	engine := tempEngine(t)
	// Append out of order; Read must return by Seq ascending.
	if err := AppendTrajectory(engine, core.TrajectorySlot{SessionID: 7, Seq: 2, EventType: "tool_call", Payload: "b", Timestamp: 200}); err != nil {
		t.Fatalf("append seq2: %v", err)
	}
	if err := AppendTrajectory(engine, core.TrajectorySlot{SessionID: 7, Seq: 1, EventType: "turn_start", Payload: "a", Timestamp: 100}); err != nil {
		t.Fatalf("append seq1: %v", err)
	}
	// Another session must not leak in.
	if err := AppendTrajectory(engine, core.TrajectorySlot{SessionID: 8, Seq: 1, EventType: "turn_start", Payload: "other", Timestamp: 100}); err != nil {
		t.Fatalf("append other: %v", err)
	}
	events, err := ReadTrajectory(engine, 7)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 2 || events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("order mismatch: %+v", events)
	}
	if events[0].Payload != "a" || events[1].Payload != "b" {
		t.Fatalf("payload mismatch")
	}
}

func TestDeleteTrajectory(t *testing.T) {
	engine := tempEngine(t)
	for i := uint64(1); i <= 3; i++ {
		if err := AppendTrajectory(engine, core.TrajectorySlot{SessionID: 5, Seq: i, EventType: "turn_start", Timestamp: int64(i)}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := DeleteTrajectory(engine, 5); err != nil {
		t.Fatalf("delete: %v", err)
	}
	events, err := ReadTrajectory(engine, 5)
	if err != nil {
		t.Fatalf("read after delete: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("want empty trajectory, got %d events", len(events))
	}
	// Deleting a session without trajectory is a no-op.
	if err := DeleteTrajectory(engine, 999); err != nil {
		t.Fatalf("delete missing session: %v", err)
	}
}

func TestCreateChainL5WithPathAndSteps(t *testing.T) {
	engine := tempEngine(t)
	path := "session:abc"
	chainID, err := CreateChainL5WithPath(engine, "整理代码", "用户要求重构", &path)
	if err != nil {
		t.Fatalf("create chain: %v", err)
	}
	chain, err := GetChainL5(engine, common.FormatHash(chainID))
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if chain.Path == nil || *chain.Path != "session:abc" {
		t.Fatalf("path not persisted: %+v", chain)
	}
	params := `{"file":"a.go"}`
	if _, err := CreateStepL5(engine, chainID, 1, "read_file", &params); err != nil {
		t.Fatalf("create step: %v", err)
	}
	steps := core.CollectAllActionSteps(engine)
	if len(steps) != 1 || steps[0].ChainID != chainID || steps[0].StepOrder != 1 || steps[0].Action != "read_file" {
		t.Fatalf("step mismatch: %+v", steps)
	}
	if steps[0].Parameters == nil || *steps[0].Parameters != params {
		t.Fatalf("step parameters mismatch")
	}
}
