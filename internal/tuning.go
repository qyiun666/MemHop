// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

// tuning.go hosts the engine's internal tuning constants. These values were
// previously exposed as MemHopDefaults fields; they are now package-private
// so hosts only configure the three business knobs (Capacity,
// DreamCompressMinTopics, SearchDreamContextThreshold) in defaults.go.
// Values equal the former defaults — nothing changes behaviorally.

const (
	// Retrieval scoring.
	rrfK             float32 = 60.0 // RRF fusion constant
	activationBonus  float32 = 0.2  // active-scene score bonus
	recentChatBonus  float32 = 0.1  // latest-timestamp scene score bonus
	minSceneScore    float32 = 1.0  // winning scene must exceed this
	vectorMinScore   float32 = 0.5  // cosine floor for the vector fallback
	vectorFloorScale float32 = 0.5  // vector floor = threshold + cosine*scale (kept below RRF+keyword reach)
	// L1 decay.
	lambdaNode              float32 = 0.01
	lambdaEdge              float32 = 0.02
	nodeRemoveThreshold     float32 = 0.05
	nodePruneEdgesThreshold float32 = 0.15
	edgeRemoveThreshold     float32 = 0.05
	minEdgeNodes            int     = 2
	// L1 scene hypergraph / spreading activation.
	l1EdgeMinSimilarity   float32 = 0.15
	l1EdgeMaxHops         int     = 2
	l1ActivationDampening float32 = 0.5
	l1ActivationThreshold float32 = 0.05
	l1AssocMaxScenes      int     = 3
	// Infrastructure.
	defaultTTLMs           int64  = 3600000 // 1 hour: scene-usage feedback window
	defaultTokenizerEngine string = "auto"
)
