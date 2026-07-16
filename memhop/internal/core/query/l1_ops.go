// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L1 graph loading operations.

package query

import (
	"encoding/json"

	"github.com/qyiun666/memhop/memhop/internal/core/model"
	"github.com/qyiun666/memhop/memhop/internal/core/storage"
	"github.com/qyiun666/memhop/memhop/internal/hash"
)

// LoadL1Graph traverses the engine and builds the full L1 visualization graph.
func LoadL1Graph(
	engine *storage.StorageEngine,
	sceneFilter *uint64,
) (*L1Graph, error) {
	var nodes []L1Node
	var edges []L1Edge
	engine.IterIndex(func(idHash, _ uint64) bool {
		rt, data, err := engine.ReadRecord(idHash)
		if err != nil {
			return true
		}
		switch rt {
		case storage.RecL1SceneNode:
			n := loadL1Node(engine, data, sceneFilter)
			if n != nil {
				nodes = append(nodes, *n)
			}
		case storage.RecL1Hyperedge:
			e := loadL1Edge(data)
			if e != nil {
				edges = append(edges, *e)
			}
		}
		return true
	})
	filterEdgesToNodes(&edges, nodes)
	if nodes == nil {
		nodes = []L1Node{}
	}
	if edges == nil {
		edges = []L1Edge{}
	}
	return &L1Graph{Nodes: nodes, Edges: edges}, nil
}

// ParseSceneFilter converts a 16-char hex scene ID to a uint64 pointer.
func ParseSceneFilter(sceneID *string) *uint64 {
	if sceneID == nil || len(*sceneID) != 16 {
		return nil
	}
	if v, err := hash.ParseID(*sceneID); err == nil {
		return &v
	}
	return nil
}

func loadL1Node(
	engine *storage.StorageEngine,
	data []byte,
	sceneFilter *uint64,
) *L1Node {
	var sn model.SceneNode
	if json.Unmarshal(data, &sn) != nil {
		return nil
	}
	if sceneFilter != nil && sn.SceneID != *sceneFilter {
		return nil
	}
	summary, kw := resolveL2ContextInfo(engine, sn.TopicIDs)
	emotion := deriveNodeEmotion(sn.Valence, sn.Arousal)
	return &L1Node{
		ID:              hash.FormatHash(sn.IDHash),
		SceneID:         hash.FormatHash(sn.SceneID),
		TopicIDs:        formatIDSlice(sn.TopicIDs),
		Depth:           sn.Depth,
		Importance:      sn.Importance,
		Valence:         sn.Valence,
		Arousal:         sn.Arousal,
		Summary:         summary,
		DominantEmotion: emotion,
		Keywords:        kw,
		RecallScore:     sn.Importance,
		CreatedAt:       sn.CreatedAt,
		UpdatedAt:       sn.UpdatedAt,
		EdgeIDs:         formatIDSlice(sn.EdgeIDs),
	}
}

func loadL1Edge(data []byte) *L1Edge {
	var se model.SceneEdge
	if json.Unmarshal(data, &se) != nil {
		return nil
	}
	return &L1Edge{
		ID:        hash.FormatHash(se.IDHash),
		Kind:      se.Kind.String(),
		NodeIDs:   formatIDSlice(se.NodeIDs),
		Weight:    se.Weight,
		CreatedAt: se.CreatedAt,
	}
}

func resolveL2ContextInfo(
	engine *storage.StorageEngine,
	topicIDs []uint64,
) (*string, []string) {
	if len(topicIDs) == 0 {
		return nil, []string{}
	}
	_, data, err := engine.ReadRecord(topicIDs[0])
	if err != nil {
		return nil, []string{}
	}
	var ctx model.TopicSlot
	if json.Unmarshal(data, &ctx) != nil {
		return nil, []string{}
	}
	kw := mergeKeywords(ctx.UserKeywords, ctx.AgentKeywords)
	return ctx.FusedSummary, kw
}

func mergeKeywords(a, b []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, k := range a {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	for _, k := range b {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}

func deriveNodeEmotion(valence, arousal float64) *string {
	var label string
	switch {
	case valence > 0.3:
		label = "positive"
	case valence < -0.3:
		label = "negative"
	case arousal > 0.6:
		label = "exciting"
	default:
		label = "neutral"
	}
	return &label
}

func formatIDSlice(ids []uint64) []string { return hash.FormatIDs(ids) }

func filterEdgesToNodes(edges *[]L1Edge, nodes []L1Node) {
	if len(nodes) == 0 || edges == nil {
		return
	}
	nodeSet := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		nodeSet[n.ID] = struct{}{}
	}
	filtered := make([]L1Edge, 0, len(*edges))
	for _, e := range *edges {
		for _, nid := range e.NodeIDs {
			if _, ok := nodeSet[nid]; ok {
				filtered = append(filtered, e)
				break
			}
		}
	}
	*edges = filtered
}
