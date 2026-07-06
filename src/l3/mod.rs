// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L3 Hypergraph Engine — storage, indexing, and query for L3 hypergraph nodes/edges.
//! Nodes/edges link to parent HypergraphSlot via graph_id; all registered in global BTreeIndex.

pub mod cache;
pub mod community;
pub mod degree;
pub mod dsl;
pub mod index;
pub mod store;

pub use cache::{AdjacencyCache, GraphAdjacency};
pub use community::{Community, CommunityConfig, CommunityResult};
pub use degree::{DegreeTracker, GraphDegrees, IsolatedNode, IsolatedResult};
pub use index::{L3Index, L3IndexQuery};
pub use store::{
    add_edge, add_node, bfs_traversal_cached, collect_l2_refs, delete_graph, delete_node,
    list_edges_by_graph, list_nodes_by_graph, remove_l3_ref_from_context,
};
