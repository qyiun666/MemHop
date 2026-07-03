// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! pest-based parser for the L3 hypergraph query DSL.
//!
//! Translates a query string into a typed `Query` AST via a PEG grammar.

use pest::Parser;
use pest_derive::Parser;

use crate::l3::dsl::ast::*;
use crate::MemHopError;

/// The pest-generated parser for our DSL grammar.
#[derive(Parser)]
#[grammar = "l3/dsl/grammar.pest"]
pub struct DslParser;

/// Parse a DSL query string into an AST.
pub fn parse(input: &str) -> Result<Query, MemHopError> {
    let pairs = DslParser::parse(Rule::query, input)
        .map_err(|e| MemHopError::DslParseError(e.to_string()))?;

    let pair = pairs
        .into_iter()
        .next()
        .ok_or_else(|| MemHopError::DslParseError("empty query".to_string()))?;

    build_query(pair)
}

// ── Top-level dispatch ─────────────────────────────────────────────────────

fn build_query(pair: pest::iterators::Pair<Rule>) -> Result<Query, MemHopError> {
    // SOI/EOI appear in the inner pairs, find the actual query_clause
    let query_clause = pair
        .into_inner()
        .find(|p| p.as_rule() == Rule::query_clause)
        .ok_or_else(|| MemHopError::DslParseError("empty query body".to_string()))?;

    // query_clause contains the matched alternative (match_clause, etc)
    let inner = query_clause
        .into_inner()
        .next()
        .ok_or_else(|| MemHopError::DslParseError("empty query body".to_string()))?;

    match inner.as_rule() {
        Rule::match_clause => build_match(inner).map(Query::Match),
        Rule::hyperedge_clause => build_hyperedge(inner).map(Query::Hyperedge),
        Rule::path_clause => build_path(inner).map(Query::Path),
        Rule::subgraph_clause => build_subgraph(inner).map(Query::Subgraph),
        _ => Err(MemHopError::DslParseError(format!(
            "unexpected rule: {:?}",
            inner.as_rule()
        ))),
    }
}

// ── MATCH (n:concept) WHERE ... RETURN n LIMIT 10 ──────────────────────────

fn build_match(pair: pest::iterators::Pair<Rule>) -> Result<NodeMatch, MemHopError> {
    let children: Vec<_> = pair.into_inner().collect();

    let variable = find_string(&children, Rule::variable);
    let node_type = find_string(&children, Rule::type_label);
    let where_clause = children
        .iter()
        .find(|p| p.as_rule() == Rule::where_clause)
        .map(|p| build_where(p.clone()))
        .transpose()?;
    let limit = find_integer(
        &children
            .iter()
            .find(|p| p.as_rule() == Rule::limit_clause)
            .map(|p| p.clone().into_inner().collect()),
    );

    Ok(NodeMatch {
        variable,
        node_type,
        where_clause,
        limit,
    })
}

// ── MATCH HYPEREDGE e-[n1, n2, ...]- WHERE ... RETURN e LIMIT 10 ───────────

fn build_hyperedge(pair: pest::iterators::Pair<Rule>) -> Result<HyperedgeMatch, MemHopError> {
    let children: Vec<_> = pair.into_inner().collect();

    let edge_var = find_string(&children, Rule::variable);
    let node_vars: Vec<String> = children
        .iter()
        .find(|p| p.as_rule() == Rule::variable_list)
        .map(|p| {
            p.clone()
                .into_inner()
                .filter(|c| c.as_rule() == Rule::variable)
                .map(|c| c.as_str().to_string())
                .collect()
        })
        .unwrap_or_default();
    let where_clause = children
        .iter()
        .find(|p| p.as_rule() == Rule::where_clause)
        .map(|p| build_where(p.clone()))
        .transpose()?;
    let limit = find_integer(
        &children
            .iter()
            .find(|p| p.as_rule() == Rule::limit_clause)
            .map(|p| p.clone().into_inner().collect()),
    );

    Ok(HyperedgeMatch {
        edge_var,
        node_vars,
        where_clause,
        limit,
    })
}

// ── PATH FROM "abc" DEPTH 3 EDGE_KINDS [...] RETURN nodes, edges ───────────

fn build_path(pair: pest::iterators::Pair<Rule>) -> Result<PathQuery, MemHopError> {
    let children: Vec<_> = pair.into_inner().collect();

    let start_node = find_string_literal(&children)
        .ok_or_else(|| MemHopError::DslParseError("missing start node in PATH".into()))?;
    let max_depth = find_integer(&Some(children.clone()))
        .ok_or_else(|| MemHopError::DslParseError("missing DEPTH in PATH".into()))?;
    let edge_kinds = children
        .iter()
        .find(|p| p.as_rule() == Rule::string_list)
        .map(|p| {
            p.clone()
                .into_inner()
                .filter(|c| c.as_rule() == Rule::string_literal)
                .map(|c| c.as_str().trim_matches('"').to_string())
                .collect()
        });

    Ok(PathQuery {
        start_node,
        max_depth,
        edge_kinds,
    })
}

// ── SUBGRAPH FROM "abc" DEPTH 2 RETURN nodes, edges ────────────────────────

fn build_subgraph(pair: pest::iterators::Pair<Rule>) -> Result<SubgraphQuery, MemHopError> {
    let children: Vec<_> = pair.into_inner().collect();

    let start_node = find_string_literal(&children)
        .ok_or_else(|| MemHopError::DslParseError("missing start node in SUBGRAPH".into()))?;
    let max_depth = find_integer(&Some(children))
        .ok_or_else(|| MemHopError::DslParseError("missing DEPTH in SUBGRAPH".into()))?;

    Ok(SubgraphQuery {
        start_node,
        max_depth,
    })
}

// ── WHERE clause ───────────────────────────────────────────────────────────

fn build_where(pair: pest::iterators::Pair<Rule>) -> Result<WhereCondition, MemHopError> {
    // Skip "WHERE" keyword, find the condition
    let condition = pair
        .into_inner()
        .find(|p| p.as_rule() == Rule::condition)
        .ok_or_else(|| MemHopError::DslParseError("empty WHERE".into()))?;
    build_condition(condition)
}

fn build_condition(pair: pest::iterators::Pair<Rule>) -> Result<WhereCondition, MemHopError> {
    match pair.as_rule() {
        Rule::condition => {
            // condition → or_condition
            let inner = pair
                .into_inner()
                .next()
                .ok_or_else(|| MemHopError::DslParseError("empty condition".into()))?;
            build_condition(inner)
        }
        Rule::or_condition => {
            let mut parts = pair.into_inner();
            let first = build_condition(
                parts
                    .next()
                    .ok_or_else(|| MemHopError::DslParseError("empty OR".into()))?,
            )?;
            let mut result = first;
            for right_pair in parts {
                let right = build_condition(right_pair)?;
                result = WhereCondition::Or(Box::new(result), Box::new(right));
            }
            Ok(result)
        }
        Rule::and_condition => {
            let mut parts = pair.into_inner();
            let first = build_condition(
                parts
                    .next()
                    .ok_or_else(|| MemHopError::DslParseError("empty AND".into()))?,
            )?;
            let mut result = first;
            for right_pair in parts {
                let right = build_condition(right_pair)?;
                result = WhereCondition::And(Box::new(result), Box::new(right));
            }
            Ok(result)
        }
        Rule::property_compare => {
            let children: Vec<_> = pair.into_inner().collect();
            let property = find_string(&children, Rule::property_name)
                .ok_or_else(|| MemHopError::DslParseError("missing property name".into()))?;
            let op_str = children
                .iter()
                .find(|p| p.as_rule() == Rule::compare_op)
                .map(|p| p.as_str())
                .ok_or_else(|| MemHopError::DslParseError("missing operator".into()))?;
            let value = children
                .iter()
                .find(|p| p.as_rule() == Rule::number)
                .and_then(|p| p.as_str().parse::<f32>().ok())
                .ok_or_else(|| MemHopError::DslParseError("missing value".into()))?;

            let operator = match op_str {
                ">" => CompareOp::Gt,
                ">=" => CompareOp::Ge,
                "<" => CompareOp::Lt,
                "<=" => CompareOp::Le,
                "=" => CompareOp::Eq,
                "!=" => CompareOp::Ne,
                _ => {
                    return Err(MemHopError::DslParseError(format!(
                        "unknown operator: {}",
                        op_str
                    )))
                }
            };

            Ok(WhereCondition::PropertyCompare {
                property,
                operator,
                value,
            })
        }
        Rule::type_equals => {
            let val = pair
                .into_inner()
                .find(|p| p.as_rule() == Rule::string_literal)
                .map(|p| p.as_str().trim_matches('"').to_string())
                .ok_or_else(|| MemHopError::DslParseError("missing type value".into()))?;
            Ok(WhereCondition::TypeEquals(val))
        }
        Rule::keyword_contains => {
            let val = pair
                .into_inner()
                .find(|p| p.as_rule() == Rule::string_literal)
                .map(|p| p.as_str().trim_matches('"').to_string())
                .ok_or_else(|| MemHopError::DslParseError("missing keyword".into()))?;
            Ok(WhereCondition::KeywordContains(val))
        }
        Rule::primary => {
            let inner = pair
                .into_inner()
                .next()
                .ok_or_else(|| MemHopError::DslParseError("empty primary condition".into()))?;
            build_condition(inner)
        }
        _ => Err(MemHopError::DslParseError(format!(
            "unexpected condition rule: {:?}",
            pair.as_rule()
        ))),
    }
}

// ── Helpers ────────────────────────────────────────────────────────────────

fn find_string(children: &[pest::iterators::Pair<Rule>], rule: Rule) -> Option<String> {
    children
        .iter()
        .find(|p| p.as_rule() == rule)
        .map(|p| p.as_str().to_string())
}

fn find_string_literal(children: &[pest::iterators::Pair<Rule>]) -> Option<String> {
    children
        .iter()
        .find(|p| p.as_rule() == Rule::string_literal)
        .map(|p| p.as_str().trim_matches('"').to_string())
}

fn find_integer(children: &Option<Vec<pest::iterators::Pair<Rule>>>) -> Option<usize> {
    children.as_ref().and_then(|c| {
        c.iter()
            .find(|p| p.as_rule() == Rule::integer)
            .and_then(|p| p.as_str().parse::<usize>().ok())
    })
}

// ── Tests ──────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_simple_match() {
        let q = parse("MATCH (n:concept)").unwrap();
        match q {
            Query::Match(m) => {
                assert_eq!(m.variable.as_deref(), Some("n"));
                assert_eq!(m.node_type.as_deref(), Some("concept"));
            }
            _ => panic!("expected Match, got {:?}", q),
        }
    }

    #[test]
    fn test_parse_match_with_where() {
        let q = parse("MATCH (n) WHERE n.importance > 0.5 LIMIT 10").unwrap();
        match q {
            Query::Match(m) => {
                assert!(m.where_clause.is_some());
                assert_eq!(m.limit, Some(10));
            }
            _ => panic!("expected Match"),
        }
    }

    #[test]
    fn test_parse_hyperedge() {
        let q = parse("MATCH HYPEREDGE e-[n1, n2, n3]-").unwrap();
        match q {
            Query::Hyperedge(h) => {
                assert_eq!(h.edge_var.as_deref(), Some("e"));
                assert_eq!(h.node_vars.len(), 3);
            }
            _ => panic!("expected Hyperedge"),
        }
    }

    #[test]
    fn test_parse_path() {
        let q = parse(r#"PATH FROM "abc123" DEPTH 3"#).unwrap();
        match q {
            Query::Path(p) => {
                assert_eq!(p.start_node, "abc123");
                assert_eq!(p.max_depth, 3);
            }
            _ => panic!("expected Path"),
        }
    }

    #[test]
    fn test_parse_subgraph() {
        let q = parse(r#"SUBGRAPH FROM "abc123" DEPTH 2"#).unwrap();
        match q {
            Query::Subgraph(s) => {
                assert_eq!(s.start_node, "abc123");
                assert_eq!(s.max_depth, 2);
            }
            _ => panic!("expected Subgraph"),
        }
    }

    #[test]
    fn test_parse_invalid() {
        assert!(parse("INVALID QUERY").is_err());
    }

    #[test]
    fn test_parse_match_no_type() {
        let q = parse("MATCH (n)").unwrap();
        match q {
            Query::Match(m) => {
                assert_eq!(m.variable.as_deref(), Some("n"));
                assert_eq!(m.node_type, None);
            }
            _ => panic!("expected Match"),
        }
    }

    #[test]
    fn test_parse_where_and() {
        let q = parse("MATCH (n) WHERE n.importance > 0.5 AND n.version >= 1").unwrap();
        match q {
            Query::Match(m) => {
                assert!(m.where_clause.is_some());
            }
            _ => panic!("expected Match"),
        }
    }
}
