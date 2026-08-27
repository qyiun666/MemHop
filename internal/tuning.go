// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

// tuning.go hosts the composition root's internal tuning constants (the
// retrieval/activation constants live with the scenefind capability that
// owns them). These values were previously exposed as MemHopDefaults fields;
// they are now package-private so hosts only configure the three business
// knobs (Capacity, DreamCompressMinTopics, SearchDreamContextThreshold) in
// defaults.go. Values equal the former defaults — nothing changes
// behaviorally.

const (
	// Retrieval gating (Search scene winner).
	minSceneScore float32 = 1.0 // winning scene must exceed this
	// L1 decay.
	lambdaNode              float32 = 0.01
	lambdaEdge              float32 = 0.02
	nodeRemoveThreshold     float32 = 0.05
	nodePruneEdgesThreshold float32 = 0.15
	edgeRemoveThreshold     float32 = 0.05
	minEdgeNodes            int     = 2
	// L1 scene hypergraph construction.
	l1EdgeMinSimilarity float32 = 0.15
	// Infrastructure.
	defaultTTLMs           int64  = 3600000 // 1 hour: scene-usage feedback window
	defaultTokenizerEngine string = "auto"
)
