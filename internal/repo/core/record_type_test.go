// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package core

import (
	"testing"

	"github.com/qyiun666/MemHop/internal/common"
)

// One id names exactly one record type. A typed reader that ignored the frame's
// record type would decode a node's JSON into a graph slot (both carry
// id_hash), and the caller would then write a slot over the node — turning a
// wrong-id call into silent data loss instead of "not found".
func TestTypedReadersRejectForeignRecordType(t *testing.T) {
	engine, err := Create(tempPath(t, "typed_read"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close(&IndexSnapshotData{}) })

	const nodeID = uint64(7001)
	if err := WriteHypergraphNode(engine, DefaultAgentID, nodeID, &HypergraphNode{
		IDHash: nodeID, GraphID: 555, Title: "node-7001",
	}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		read func() error
	}{
		{"graph slot over node", func() error { _, err := ReadGraphSlot(engine, DefaultAgentID, nodeID); return err }},
		{"capability over node", func() error { _, err := ReadCapability(engine, DefaultAgentID, nodeID); return err }},
		{"archive over node", func() error { _, err := ReadArchiveSlot(engine, DefaultAgentID, nodeID); return err }},
		{"scene slot over node", func() error { _, err := ReadSceneSlot(engine, DefaultAgentID, nodeID); return err }},
	}
	for _, tc := range cases {
		err := tc.read()
		if err == nil {
			t.Fatalf("%s: expected an error, got none", tc.name)
		}
		if common.CodeOf(err) != common.ErrNotFound {
			t.Fatalf("%s: expected ErrNotFound, got %v", tc.name, err)
		}
	}

	got, err := ReadHypergraphNode(engine, DefaultAgentID, nodeID)
	if err != nil {
		t.Fatalf("read node back: %v", err)
	}
	if got.Title != "node-7001" {
		t.Fatalf("node title: %q", got.Title)
	}
}
