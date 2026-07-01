//! Query result types for the L3 hypergraph query DSL.

use crate::query::types::{Subgraph, TraversalHop};
use crate::slot::hypergraph::{HypergraphEdge, HypergraphNode};
use serde::{Deserialize, Serialize};

/// The result of executing a DSL query.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum QueryResult {
    /// MATCH node result
    Nodes {
        items: Vec<HypergraphNode>,
        total: usize,
    },
    /// MATCH HYPEREDGE result
    Edges {
        items: Vec<HypergraphEdge>,
        total: usize,
    },
    /// PATH traversal result
    Hops {
        items: Vec<TraversalHop>,
        total: usize,
    },
    /// SUBGRAPH extraction result
    Subgraph(Subgraph),
}
