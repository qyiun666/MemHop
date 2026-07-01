//! AST types for the L3 hypergraph query DSL.

use serde::{Deserialize, Serialize};

/// Top-level query AST node.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum Query {
    /// `MATCH (var:type) [WHERE ...] RETURN var [LIMIT n]`
    Match(NodeMatch),
    /// `MATCH HYPEREDGE e-[v1, v2, ...]- [WHERE ...] RETURN e [LIMIT n]`
    Hyperedge(HyperedgeMatch),
    /// `PATH FROM "node_id" DEPTH n [EDGE_KINDS [...]] RETURN nodes, edges`
    Path(PathQuery),
    /// `SUBGRAPH FROM "node_id" DEPTH n RETURN nodes, edges`
    Subgraph(SubgraphQuery),
}

/// Node matching query: `MATCH (n:concept) WHERE n.importance > 0.5 RETURN n LIMIT 10`
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct NodeMatch {
    /// Variable name (e.g. "n")
    pub variable: Option<String>,
    /// Optional type filter (e.g. "concept")
    pub node_type: Option<String>,
    /// Optional WHERE conditions
    pub where_clause: Option<WhereCondition>,
    /// Optional LIMIT
    pub limit: Option<usize>,
}

/// Hyperedge matching query: `MATCH HYPEREDGE e-[n1, n2]- WHERE e.weight > 0.5 RETURN e`
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HyperedgeMatch {
    /// Edge variable name (e.g. "e")
    pub edge_var: Option<String>,
    /// Node variables within the hyperedge
    pub node_vars: Vec<String>,
    /// Optional WHERE conditions
    pub where_clause: Option<WhereCondition>,
    /// Optional LIMIT
    pub limit: Option<usize>,
}

/// Path traversal query: `PATH FROM "abc" DEPTH 3 EDGE_KINDS ["Related"] RETURN nodes, edges`
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PathQuery {
    /// Hex-formatted start node ID
    pub start_node: String,
    /// Maximum traversal depth
    pub max_depth: usize,
    /// Optional edge kind filter
    pub edge_kinds: Option<Vec<String>>,
}

/// Subgraph extraction query: `SUBGRAPH FROM "abc" DEPTH 2 RETURN nodes, edges`
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SubgraphQuery {
    /// Hex-formatted start node ID
    pub start_node: String,
    /// Maximum extraction depth
    pub max_depth: usize,
}

/// WHERE clause condition tree.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum WhereCondition {
    /// Property comparison: `n.importance > 0.5`
    PropertyCompare {
        property: String,
        operator: CompareOp,
        value: f32,
    },
    /// Type equality: `n.type = "concept"`
    TypeEquals(String),
    /// Keyword contains: `n.keywords CONTAINS "rust"`
    KeywordContains(String),
    /// Logical AND
    And(Box<WhereCondition>, Box<WhereCondition>),
    /// Logical OR
    Or(Box<WhereCondition>, Box<WhereCondition>),
}

/// Comparison operators for WHERE conditions.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum CompareOp {
    Gt,
    Ge,
    Lt,
    Le,
    Eq,
    Ne,
}
