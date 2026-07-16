// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L3 HypergraphSlot + HypergraphNode + HypergraphEdge (hypergraph.rs).
// Hash fields on Node/Edge use 16-char hex JSON to match Rust serde output.

package model

import (
	"encoding/json"
	"fmt"

	"github.com/qyiun666/memhop/memhop/internal/hash"
)

// ============================================================================
// HypergraphSource — how the hypergraph was created
// ============================================================================

// HypergraphSource represents the origin of an L3 hypergraph.
// JSON format matches Rust's externally-tagged serde enum:
//
//	{"Path": "/src/lib.rs"} | {"Context": 12345} | {"Url": "https://..."} | "Manual"
type HypergraphSource struct {
	Kind  SourceKind
	Value string // path or URL string; empty for Manual
	// ContextID is used when Kind == SourceContext
	ContextID uint64
}

func (s HypergraphSource) MarshalJSON() ([]byte, error) {
	switch s.Kind {
	case SourcePath:
		return json.Marshal(map[string]string{"Path": s.Value})
	case SourceContext:
		return json.Marshal(map[string]uint64{"Context": s.ContextID})
	case SourceURL:
		return json.Marshal(map[string]string{"Url": s.Value})
	case SourceManual:
		return json.Marshal("Manual")
	default:
		return nil, fmt.Errorf("unknown SourceKind: %d", s.Kind)
	}
}

func (s *HypergraphSource) UnmarshalJSON(data []byte) error {
	// Try string first (Manual variant)
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		if str == "Manual" {
			*s = HypergraphSource{Kind: SourceManual}
			return nil
		}
		return fmt.Errorf("unknown HypergraphSource string: %q", str)
	}
	// Try object variants
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["Path"]; ok {
		var p string
		if err := json.Unmarshal(v, &p); err != nil {
			return err
		}
		*s = HypergraphSource{Kind: SourcePath, Value: p}
		return nil
	}
	if v, ok := raw["Context"]; ok {
		var c uint64
		if err := json.Unmarshal(v, &c); err != nil {
			return err
		}
		*s = HypergraphSource{Kind: SourceContext, ContextID: c}
		return nil
	}
	if v, ok := raw["Url"]; ok {
		var u string
		if err := json.Unmarshal(v, &u); err != nil {
			return err
		}
		*s = HypergraphSource{Kind: SourceURL, Value: u}
		return nil
	}
	return fmt.Errorf("unknown HypergraphSource variant")
}

// ============================================================================
// HypergraphSlot — container metadata
// ============================================================================

// HypergraphSlot holds L3 hypergraph container metadata.
type HypergraphSlot struct {
	IDHash    uint64           `json:"id_hash"`
	Name      string           `json:"name"`
	Source    HypergraphSource `json:"source"`
	NodeCount uint32           `json:"node_count"`
	EdgeCount uint32           `json:"edge_count"`
	CreatedAt int64            `json:"created_at"`
	UpdatedAt int64            `json:"updated_at"`
	Version   uint32           `json:"version"`
}

// ============================================================================
// HypergraphNode — id_hash and graph_id are hex-serialized in JSON
// ============================================================================

// HypergraphNode is a node within an L3 hypergraph.
type HypergraphNode struct {
	IDHash     uint64   `json:"id_hash"`
	GraphID    uint64   `json:"graph_id"`
	Title      string   `json:"title"`
	NodeType   string   `json:"node_type"`
	Content    string   `json:"content"`
	Keywords   []string `json:"keywords"`
	SourceRef  *string  `json:"source_ref,omitempty"`
	Importance float32  `json:"importance"`
	ValidFrom  int64    `json:"valid_from"`
	ValidUntil int64    `json:"valid_until"`
	Summary    *string  `json:"summary,omitempty"`
	CreatedAt  int64    `json:"created_at"`
	UpdatedAt  int64    `json:"updated_at"`
	Version    uint32   `json:"version"`
}

// hypergraphNodeJSON is the JSON representation with hex-encoded hash fields.
type hypergraphNodeJSON struct {
	IDHash     string   `json:"id_hash"`
	GraphID    string   `json:"graph_id"`
	Title      string   `json:"title"`
	NodeType   string   `json:"node_type"`
	Content    string   `json:"content"`
	Keywords   []string `json:"keywords"`
	SourceRef  *string  `json:"source_ref,omitempty"`
	Importance float32  `json:"importance"`
	ValidFrom  int64    `json:"valid_from"`
	ValidUntil int64    `json:"valid_until"`
	Summary    *string  `json:"summary,omitempty"`
	CreatedAt  int64    `json:"created_at"`
	UpdatedAt  int64    `json:"updated_at"`
	Version    uint32   `json:"version"`
}

// MarshalJSON serializes HypergraphNode with hex-encoded hash fields.
func (n HypergraphNode) MarshalJSON() ([]byte, error) {
	return json.Marshal(hypergraphNodeJSON{
		IDHash: hash.FormatHash(n.IDHash), GraphID: hash.FormatHash(n.GraphID),
		Title: n.Title, NodeType: n.NodeType, Content: n.Content,
		Keywords: n.Keywords, SourceRef: n.SourceRef, Importance: n.Importance,
		ValidFrom: n.ValidFrom, ValidUntil: n.ValidUntil, Summary: n.Summary,
		CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, Version: n.Version,
	})
}

// UnmarshalJSON deserializes HypergraphNode from hex-encoded hash fields.
func (n *HypergraphNode) UnmarshalJSON(data []byte) error {
	var j hypergraphNodeJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	id, err := hash.ParseID(j.IDHash)
	if err != nil {
		return fmt.Errorf("parse id_hash: %w", err)
	}
	gid, err := hash.ParseID(j.GraphID)
	if err != nil {
		return fmt.Errorf("parse graph_id: %w", err)
	}
	n.IDHash, n.GraphID = id, gid
	n.Title, n.NodeType, n.Content = j.Title, j.NodeType, j.Content
	n.Keywords, n.SourceRef = j.Keywords, j.SourceRef
	n.Importance = j.Importance
	n.ValidFrom, n.ValidUntil = j.ValidFrom, j.ValidUntil
	n.Summary = j.Summary
	n.CreatedAt, n.UpdatedAt, n.Version = j.CreatedAt, j.UpdatedAt, j.Version
	return nil
}

// ============================================================================
// HypergraphEdge — id_hash, graph_id, node_ids are hex-serialized in JSON
// ============================================================================

// HypergraphEdge is an edge within an L3 hypergraph (supports hyperedges).
type HypergraphEdge struct {
	IDHash      uint64        `json:"id_hash"`
	GraphID     uint64        `json:"graph_id"`
	Kind        GraphEdgeKind `json:"kind"`
	NodeIDs     []uint64      `json:"node_ids"`
	Weight      float32       `json:"weight"`
	Label       *string       `json:"label,omitempty"`
	Description *string       `json:"description,omitempty"`
	Confidence  float32       `json:"confidence"`
	ValidFrom   int64         `json:"valid_from"`
	ValidUntil  int64         `json:"valid_until"`
	CreatedAt   int64         `json:"created_at"`
}

// hypergraphEdgeJSON is the JSON wire format with hex-encoded hashes.
type hypergraphEdgeJSON struct {
	IDHash      string        `json:"id_hash"`
	GraphID     string        `json:"graph_id"`
	Kind        GraphEdgeKind `json:"kind"`
	NodeIDs     []string      `json:"node_ids"`
	Weight      float32       `json:"weight"`
	Label       *string       `json:"label,omitempty"`
	Description *string       `json:"description,omitempty"`
	Confidence  float32       `json:"confidence"`
	ValidFrom   int64         `json:"valid_from"`
	ValidUntil  int64         `json:"valid_until"`
	CreatedAt   int64         `json:"created_at"`
}

// MarshalJSON serializes HypergraphEdge with hex-encoded hash fields.
func (e HypergraphEdge) MarshalJSON() ([]byte, error) {
	hexIDs := make([]string, len(e.NodeIDs))
	for i, id := range e.NodeIDs {
		hexIDs[i] = hash.FormatHash(id)
	}
	return json.Marshal(hypergraphEdgeJSON{
		IDHash: hash.FormatHash(e.IDHash), GraphID: hash.FormatHash(e.GraphID),
		Kind: e.Kind, NodeIDs: hexIDs, Weight: e.Weight,
		Label: e.Label, Description: e.Description, Confidence: e.Confidence,
		ValidFrom: e.ValidFrom, ValidUntil: e.ValidUntil, CreatedAt: e.CreatedAt,
	})
}

// UnmarshalJSON deserializes HypergraphEdge from hex-encoded hash fields.
func (e *HypergraphEdge) UnmarshalJSON(data []byte) error {
	var j hypergraphEdgeJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	id, err := hash.ParseID(j.IDHash)
	if err != nil {
		return fmt.Errorf("parse id_hash: %w", err)
	}
	gid, err := hash.ParseID(j.GraphID)
	if err != nil {
		return fmt.Errorf("parse graph_id: %w", err)
	}
	nodeIDs := make([]uint64, len(j.NodeIDs))
	for i, h := range j.NodeIDs {
		nodeIDs[i], err = hash.ParseID(h)
		if err != nil {
			return fmt.Errorf("parse node_ids[%d]: %w", i, err)
		}
	}
	e.IDHash, e.GraphID = id, gid
	e.Kind, e.NodeIDs, e.Weight = j.Kind, nodeIDs, j.Weight
	e.Label, e.Description = j.Label, j.Description
	e.Confidence = j.Confidence
	e.ValidFrom, e.ValidUntil, e.CreatedAt = j.ValidFrom, j.ValidUntil, j.CreatedAt
	return nil
}
