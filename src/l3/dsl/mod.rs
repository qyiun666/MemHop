//! L3 Hypergraph Query DSL — Cypher-inspired read-only query language.
//!
//! # Syntax (V1)
//!
//! ```text
//! MATCH (n:concept) WHERE n.importance > 0.5 RETURN n LIMIT 10
//! MATCH HYPEREDGE e-[n1, n2, n3]- RETURN e
//! PATH FROM "abc123" DEPTH 3 EDGE_KINDS ["Related"] RETURN nodes, edges
//! SUBGRAPH FROM "abc123" DEPTH 2 RETURN nodes, edges
//! ```
//!
//! # Architecture
//!
//! ```text
//! Query string → pest parser → AST → executor → store.rs functions
//! ```

pub mod ast;
pub mod executor;
pub mod parser;
pub mod result;

pub use ast::*;
pub use result::QueryResult;
