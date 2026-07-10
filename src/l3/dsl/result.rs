// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Query result types for the L3 hypergraph query DSL.

use crate::query::types::{GraphEdge, GraphNode, Subgraph, TraversalHop};
use serde::{Deserialize, Serialize};

/// The result of executing a DSL query.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum QueryResult {
    /// MATCH node result
    Nodes { items: Vec<GraphNode>, total: usize },
    /// MATCH HYPEREDGE result
    Edges { items: Vec<GraphEdge>, total: usize },
    /// PATH traversal result
    Hops {
        items: Vec<TraversalHop>,
        total: usize,
    },
    /// SUBGRAPH extraction result
    Subgraph(Subgraph),
}
