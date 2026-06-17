//! L3 Hypergraph Engine
//!
//! Provides storage, indexing, and query capabilities for L3 hypergraph nodes and edges.
//!
//! # Architecture
//!
//! ```text
//! HypergraphSlot (container metadata, stored in BTreeIndex)
//!   ├─ HypergraphNode (entity/concept/event, stored per-page in BTreeIndex)
//!   └─ HypergraphEdge (hyperedge connecting ≥2 nodes, stored per-page in BTreeIndex)
//! ```
//!
//! All nodes and edges are registered in the global BTreeIndex using their own id_hash.
//! The `graph_id` field on each node/edge links them to their parent HypergraphSlot.

pub mod index;
pub mod store;

pub use index::{L3Index, L3IndexQuery};
pub use store::{
    add_edge, add_node, collect_l2_refs, count_graph_elements, delete_edge, delete_graph,
    delete_node, get_edge, get_node, list_edges_by_graph, list_nodes_by_graph, read_node_neighbors,
    remove_l3_ref_from_context,
};
