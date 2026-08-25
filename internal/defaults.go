// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

// MemHopDefaults holds all tunable engine defaults.
type MemHopDefaults struct {
	RRFK                        float32 `json:"rrf_k"`
	ActivationBonus             float32 `json:"activation_bonus"`
	RecentChatBonus             float32 `json:"recent_chat_bonus"`
	LambdaNode                  float32 `json:"lambda_node"`
	LambdaEdge                  float32 `json:"lambda_edge"`
	NodeRemoveThreshold         float32 `json:"node_remove_threshold"`
	NodePruneEdgesThreshold     float32 `json:"node_prune_edges_threshold"`
	EdgeRemoveThreshold         float32 `json:"edge_remove_threshold"`
	MinEdgeNodes                int     `json:"min_edge_nodes"`
	DefaultTTLMs                int64   `json:"default_ttl_ms"`
	Capacity                    int     `json:"capacity"`
	TokenizerEngine             string  `json:"tokenizer_engine,omitempty"`
	MinSceneScore               float32 `json:"min_scene_score"`
	VectorMinScore              float32 `json:"vector_min_score"`
	DreamCompressMinTopics      int     `json:"dream_compress_min_topics"`
	SearchDreamContextThreshold int     `json:"search_dream_context_threshold"`
	L1EdgeMinSimilarity         float32 `json:"l1_edge_min_similarity"`
	L1EdgeMaxHops               int     `json:"l1_edge_max_hops"`
	L1ActivationDampening       float32 `json:"l1_activation_dampening"`
	L1ActivationThreshold       float32 `json:"l1_activation_threshold"`
	L1AssocMaxScenes            int     `json:"l1_assoc_max_scenes"`
	MaxResults                  int     `json:"max_results"`
	DefaultTimeoutSecs          int     `json:"default_timeout_secs"`
	DefaultMaxOutputTokens      int     `json:"default_max_output_tokens"`
	MaxDepth                    int     `json:"max_depth"`
}

// DefaultMemHopDefaults is the single hardcoded source of engine defaults.
var DefaultMemHopDefaults = &MemHopDefaults{
	TokenizerEngine:             "auto",
	RRFK:                        60.0,
	ActivationBonus:             0.2,
	RecentChatBonus:             0.1,
	LambdaNode:                  0.01,
	LambdaEdge:                  0.02,
	NodeRemoveThreshold:         0.05,
	NodePruneEdgesThreshold:     0.15,
	EdgeRemoveThreshold:         0.05,
	MinEdgeNodes:                2,
	DefaultTTLMs:                3600000, // 1 hour
	Capacity:                    7,
	MinSceneScore:               1.0,
	VectorMinScore:              0.5,
	DreamCompressMinTopics:      20,
	SearchDreamContextThreshold: 30,   // Search triggers a scene Dream when its context exceeds this many topics
	L1EdgeMinSimilarity:         0.15, // min keyword-overlap Jaccard to create an L1 hyperedge during Dream
	L1EdgeMaxHops:               2,    // spreading-activation walk depth
	L1ActivationDampening:       0.5,  // activation decay per hop
	L1ActivationThreshold:       0.05, // activation cutoff; weaker paths stop spreading
	L1AssocMaxScenes:            3,    // associated scenes returned by spreading activation
	MaxResults:                  20,
	DefaultTimeoutSecs:          60,
	DefaultMaxOutputTokens:      8192,
	MaxDepth:                    4,
}
