// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L3 Hypergraph Query DSL — Cypher-inspired read-only query language.
//! Syntax: MATCH | MATCH HYPEREDGE | PATH | SUBGRAPH. Pipeline: query string → pest parser → AST → executor.

pub mod ast;
pub mod executor;
pub mod parser;
pub mod result;

pub use ast::*;
pub use result::QueryResult;
