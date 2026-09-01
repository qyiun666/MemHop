// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package internal

// tuning.go hosts the composition root's internal tuning constants: the L1
// decay parameters, the scene-similarity floor of hyperedge construction and
// the usage-feedback window. They are package-private by design — hosts
// configure only the business knobs in defaults.go.

const (
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
