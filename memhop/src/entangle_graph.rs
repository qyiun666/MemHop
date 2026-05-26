//! EntangleGraph — explicit association graph for v0.7.0.
//!
//! Models the brain's "trigger -> cascade" activation mechanism.
//! Memories are nodes; associations are typed weighted edges.
//! Recall is performed via seed activation + BFS spreading.

use serde::{Deserialize, Serialize};
use std::collections::{HashMap, HashSet, VecDeque};

// ── Constants ──────────────────────────────────────────────

/// Edges whose weight falls below this threshold are removed during decay / prune.
pub const EDGE_PRUNE_THRESHOLD: f32 = 0.03;

/// Target maximum average node degree. When the average degree exceeds this
/// value, `clamp_avg_degree` prunes the weakest edges to bring it back.
pub const MAX_AVG_DEGREE: usize = 30;

/// Edge type — semantic classification of an association.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum EdgeType {
    /// Semantic similarity (discovered by auto_entangle).
    Semantic,
    /// Temporal adjacency (memories adjacent within the same session).
    Temporal,
    /// Manually declared by the user.
    Manual,
    /// Cross-tree association (links between different knowledge trees).
    CrossTree,
    /// Contradiction / inhibitory edge — spreads negative activation.
    #[serde(rename = "contradiction")]
    Contradiction,
}

/// A directed edge in the graph.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Edge {
    pub target_id: String,
    /// Association strength in (0.0, 1.0].
    pub weight: f32,
    pub edge_type: EdgeType,
}

/// One entry produced by spreading activation.
#[derive(Debug, Clone)]
pub struct SpreadResult {
    pub id: String,
    /// Hop count from the seed (≥ 1).
    pub distance: usize,
    /// Multiplicative weight along the path used to reach this node.
    pub accumulated_weight: f32,
}

/// EntangleGraph — explicit association graph.
///
/// Internal representation is an adjacency list. Edges are directed; bidirectional
/// associations are stored as two opposing directed edges so per-direction
/// strengthening / decay remains independent.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EntangleGraph {
    adjacency: HashMap<String, Vec<Edge>>,
    node_count: usize,
}

impl Default for EntangleGraph {
    fn default() -> Self {
        Self::new()
    }
}

impl EntangleGraph {
    pub fn new() -> Self {
        EntangleGraph {
            adjacency: HashMap::new(),
            node_count: 0,
        }
    }

    /// Ensure a node exists in the adjacency map and update node_count accordingly.
    fn touch_node(&mut self, id: &str) {
        if !self.adjacency.contains_key(id) {
            self.adjacency.insert(id.to_string(), Vec::new());
            self.node_count += 1;
        }
    }

    /// Add a directed edge. If an edge with the same (from, to) already exists
    /// its weight and type are overwritten (last-write-wins).
    pub fn add_edge(&mut self, from: &str, to: &str, weight: f32, edge_type: EdgeType) {
        if from == to {
            return; // no self-loops
        }
        let w = clamp_weight(weight);

        self.touch_node(from);
        self.touch_node(to);

        let entry = self
            .adjacency
            .get_mut(from)
            .expect("from node was just inserted");

        if let Some(existing) = entry.iter_mut().find(|e| e.target_id == to) {
            existing.weight = w;
            existing.edge_type = edge_type;
        } else {
            entry.push(Edge {
                target_id: to.to_string(),
                weight: w,
                edge_type,
            });
        }
    }

    /// Add a bidirectional edge (two opposing directed edges).
    pub fn add_bidirectional_edge(&mut self, a: &str, b: &str, weight: f32, edge_type: EdgeType) {
        self.add_edge(a, b, weight, edge_type.clone());
        self.add_edge(b, a, weight, edge_type);
    }

    /// Remove a node and every incoming / outgoing edge that references it.
    pub fn remove_node(&mut self, id: &str) {
        if self.adjacency.remove(id).is_some() {
            self.node_count -= 1;
        }
        for edges in self.adjacency.values_mut() {
            edges.retain(|e| e.target_id != id);
        }
    }

    /// BFS spreading activation from `seed_id`.
    ///
    /// * `depth` — maximum hop count from the seed (recommend ≤ 2).
    /// * `cap`   — maximum number of returned candidates (recommend ≤ 50).
    ///
    /// Weights multiply along each path so the accumulated weight monotonically
    /// decreases the further we walk from the seed. Results are sorted by
    /// `accumulated_weight` descending.
    ///
    /// Contradiction edges (type `Contradiction`) cause the accumulated weight
    /// to be multiplied by -0.5, acting as an inhibitor along the spread path.
    pub fn spread(&self, seed_id: &str, depth: usize, cap: usize) -> Vec<SpreadResult> {
        let mut results: Vec<SpreadResult> = Vec::new();
        if cap == 0 {
            return results;
        }
        if !self.adjacency.contains_key(seed_id) {
            return results;
        }

        let mut visited: HashSet<String> = HashSet::new();
        visited.insert(seed_id.to_string());

        let mut queue: VecDeque<(String, usize, f32)> = VecDeque::new();
        queue.push_back((seed_id.to_string(), 0, 1.0));

        while let Some((current, dist, weight)) = queue.pop_front() {
            if dist > 0 {
                results.push(SpreadResult {
                    id: current.clone(),
                    distance: dist,
                    accumulated_weight: weight,
                });
            }

            if dist >= depth {
                continue;
            }

            if let Some(edges) = self.adjacency.get(&current) {
                for edge in edges {
                    if visited.contains(&edge.target_id) {
                        continue;
                    }
                    visited.insert(edge.target_id.clone());
                    let new_weight = if edge.edge_type == EdgeType::Contradiction {
                        weight * (-0.5)
                    } else {
                        weight * edge.weight
                    };
                    queue.push_back((edge.target_id.clone(), dist + 1, new_weight));
                }
            }
        }

        results.sort_by(|a, b| {
            b.accumulated_weight
                .partial_cmp(&a.accumulated_weight)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        results.truncate(cap);
        results
    }

    /// Direct outgoing neighbours.
    pub fn neighbors(&self, id: &str) -> Option<&[Edge]> {
        self.adjacency.get(id).map(|v| v.as_slice())
    }

    /// Strengthen an existing edge's weight (positive feedback).
    /// No-op when the edge does not exist. The resulting weight is clamped to (0.0, 1.0].
    pub fn strengthen(&mut self, from: &str, to: &str, delta: f32) {
        if let Some(edges) = self.adjacency.get_mut(from) {
            if let Some(edge) = edges.iter_mut().find(|e| e.target_id == to) {
                edge.weight = clamp_weight(edge.weight + delta);
            }
        }
    }

    /// Multiplicatively decay every edge's weight: `weight *= (1.0 - lambda)`.
    /// Intended to be called periodically by Dream.
    pub fn decay_all(&mut self, lambda: f32) {
        let factor = (1.0 - lambda).max(0.0);
        for edges in self.adjacency.values_mut() {
            for edge in edges.iter_mut() {
                edge.weight = clamp_weight(edge.weight * factor);
            }
        }
    }

    /// Drop edges whose weight is below `threshold`. Returns the number removed.
    pub fn prune(&mut self, threshold: f32) -> usize {
        let mut removed = 0usize;
        for edges in self.adjacency.values_mut() {
            let before = edges.len();
            edges.retain(|e| e.weight >= threshold);
            removed += before - edges.len();
        }
        removed
    }

    /// Return all contradiction pairs present in `ids`.
    ///
    /// For each id in the input slice, outgoing edges of type `Contradiction`
    /// are examined. If the target is also present in `ids`, the pair
    /// `(id, target_id)` is included in the result.
    pub fn contradiction_pairs_in<'a>(
        &'a self,
        ids: &'a [&'a str],
    ) -> Vec<(&'a str, &'a str)> {
        let id_set: HashSet<&str> = ids.iter().copied().collect();
        let mut pairs = Vec::new();
        for &id in ids {
            if let Some(edges) = self.adjacency.get(id) {
                for edge in edges {
                    if edge.edge_type == EdgeType::Contradiction
                        && id_set.contains(edge.target_id.as_str())
                    {
                        pairs.push((id, edge.target_id.as_str()));
                    }
                }
            }
        }
        pairs
    }

    /// Decay a single edge's weight by `exp(-0.05 * sqrt(hours))`.
    ///
    /// Returns `true` if the edge survives (weight ≥ `EDGE_PRUNE_THRESHOLD`),
    /// or `false` if the edge was removed or did not exist.
    pub fn decay_edge(&mut self, from: &str, to: &str, hours: f64) -> bool {
        if let Some(edges) = self.adjacency.get_mut(from) {
            let pos = edges.iter().position(|e| e.target_id == to);
            if let Some(idx) = pos {
                let factor = (-0.05_f64 * hours.sqrt()).exp();
                edges[idx].weight = clamp_weight(edges[idx].weight * factor as f32);
                if edges[idx].weight < EDGE_PRUNE_THRESHOLD {
                    edges.remove(idx);
                    return false;
                }
                return true;
            }
        }
        false
    }

    /// If the average node degree exceeds `MAX_AVG_DEGREE`, prune the weakest
    /// edges proportionally so the resulting average degree approximates
    /// `MAX_AVG_DEGREE`.
    ///
    /// For each node, outgoing edges are sorted by weight (strongest first) and
    /// only the strongest `max(1, ceil(MAX_AVG_DEGREE * degree / avg_degree))`
    /// edges are retained. Returns the number of edges removed.
    pub fn clamp_avg_degree(&mut self) -> usize {
        let n = self.node_count.max(1);
        let e = self.edge_count();
        let avg_deg = e as f32 / n as f32;
        if avg_deg <= MAX_AVG_DEGREE as f32 {
            return 0;
        }
        let mut removed = 0usize;
        for edges in self.adjacency.values_mut() {
            if edges.len() <= 1 {
                continue;
            }
            edges.sort_by(|a, b| {
                b.weight
                    .partial_cmp(&a.weight)
                    .unwrap_or(std::cmp::Ordering::Equal)
            });
            let keep = ((MAX_AVG_DEGREE as f32) * edges.len() as f32 / avg_deg)
                .ceil()
                .max(1.0)
                .min(edges.len() as f32) as usize;
            if edges.len() > keep {
                removed += edges.len() - keep;
                edges.truncate(keep);
            }
        }
        removed
    }

    pub fn node_count(&self) -> usize {
        self.node_count
    }

    pub fn edge_count(&self) -> usize {
        self.adjacency.values().map(|v| v.len()).sum()
    }

    /// Serialize the graph using bincode for LMDB persistence.
    pub fn to_bytes(&self) -> Vec<u8> {
        bincode::serialize(self).unwrap_or_default()
    }

    /// Deserialize a graph from bincode bytes.
    pub fn from_bytes(data: &[u8]) -> Result<Self, Box<dyn std::error::Error>> {
        let g: EntangleGraph = bincode::deserialize(data)?;
        Ok(g)
    }
}

/// Clamp a weight to the valid range (0.0, 1.0]. Non-finite values collapse to 0.0.
fn clamp_weight(w: f32) -> f32 {
    if !w.is_finite() {
        return 0.0;
    }
    if w < 0.0 {
        0.0
    } else if w > 1.0 {
        1.0
    } else {
        w
    }
}

// ── Tests ─────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn add_and_query_edges() {
        let mut g = EntangleGraph::new();
        g.add_edge("a", "b", 0.8, EdgeType::Semantic);
        g.add_edge("a", "c", 0.5, EdgeType::Temporal);

        assert_eq!(g.node_count(), 3);
        assert_eq!(g.edge_count(), 2);

        let nb = g.neighbors("a").unwrap();
        assert_eq!(nb.len(), 2);
    }

    #[test]
    fn add_edge_overwrites_existing() {
        let mut g = EntangleGraph::new();
        g.add_edge("a", "b", 0.4, EdgeType::Semantic);
        g.add_edge("a", "b", 0.9, EdgeType::Manual);

        let edges = g.neighbors("a").unwrap();
        assert_eq!(edges.len(), 1);
        assert!((edges[0].weight - 0.9).abs() < 1e-6);
        assert_eq!(edges[0].edge_type, EdgeType::Manual);
    }

    #[test]
    fn bidirectional_creates_both_directions() {
        let mut g = EntangleGraph::new();
        g.add_bidirectional_edge("a", "b", 0.7, EdgeType::Manual);
        assert_eq!(g.edge_count(), 2);
        assert_eq!(g.neighbors("a").unwrap().len(), 1);
        assert_eq!(g.neighbors("b").unwrap().len(), 1);
    }

    #[test]
    fn spread_walks_two_hops_with_weight_decay() {
        let mut g = EntangleGraph::new();
        g.add_edge("a", "b", 0.8, EdgeType::Semantic);
        g.add_edge("b", "c", 0.5, EdgeType::Semantic);

        let r = g.spread("a", 2, 10);
        assert_eq!(r.len(), 2);
        // sorted by accumulated_weight desc — b (0.8) before c (0.4)
        assert_eq!(r[0].id, "b");
        assert!((r[0].accumulated_weight - 0.8).abs() < 1e-6);
        assert_eq!(r[1].id, "c");
        assert!((r[1].accumulated_weight - 0.4).abs() < 1e-6);
    }

    #[test]
    fn spread_handles_cycles_via_visited_set() {
        let mut g = EntangleGraph::new();
        g.add_edge("a", "b", 0.9, EdgeType::Semantic);
        g.add_edge("b", "a", 0.9, EdgeType::Semantic);
        g.add_edge("b", "c", 0.5, EdgeType::Semantic);

        let r = g.spread("a", 3, 10);
        // Only b and c should appear (a is the seed, excluded)
        assert_eq!(r.len(), 2);
        assert!(r.iter().all(|x| x.id != "a"));
    }

    #[test]
    fn spread_respects_cap() {
        let mut g = EntangleGraph::new();
        for i in 0..10 {
            g.add_edge("seed", &format!("n{}", i), 0.5, EdgeType::Semantic);
        }
        let r = g.spread("seed", 1, 3);
        assert_eq!(r.len(), 3);
    }

    #[test]
    fn strengthen_clamps_to_one() {
        let mut g = EntangleGraph::new();
        g.add_edge("a", "b", 0.9, EdgeType::Semantic);
        g.strengthen("a", "b", 0.5);
        let edges = g.neighbors("a").unwrap();
        assert!((edges[0].weight - 1.0).abs() < 1e-6);
    }

    #[test]
    fn decay_all_multiplicatively() {
        let mut g = EntangleGraph::new();
        g.add_edge("a", "b", 0.8, EdgeType::Semantic);
        g.decay_all(0.5);
        let edges = g.neighbors("a").unwrap();
        assert!((edges[0].weight - 0.4).abs() < 1e-6);
    }

    #[test]
    fn prune_removes_weak_edges() {
        let mut g = EntangleGraph::new();
        g.add_edge("a", "b", 0.8, EdgeType::Semantic);
        g.add_edge("a", "c", 0.1, EdgeType::Semantic);
        let removed = g.prune(0.3);
        assert_eq!(removed, 1);
        assert_eq!(g.edge_count(), 1);
    }

    #[test]
    fn remove_node_drops_incoming_edges() {
        let mut g = EntangleGraph::new();
        g.add_edge("a", "b", 0.8, EdgeType::Semantic);
        g.add_edge("c", "b", 0.6, EdgeType::Semantic);
        g.remove_node("b");
        assert_eq!(g.edge_count(), 0);
        assert_eq!(g.node_count(), 2);
    }

    #[test]
    fn round_trip_serialization() {
        let mut g = EntangleGraph::new();
        g.add_edge("a", "b", 0.8, EdgeType::Semantic);
        g.add_edge("b", "c", 0.5, EdgeType::Manual);

        let bytes = g.to_bytes();
        let g2 = EntangleGraph::from_bytes(&bytes).unwrap();
        assert_eq!(g2.node_count(), 3);
        assert_eq!(g2.edge_count(), 2);
        let edges = g2.neighbors("a").unwrap();
        assert_eq!(edges[0].target_id, "b");
    }

    // ── New tests for v0.7.3 ────────────────────────────────

    #[test]
    fn test_contradiction_edge() {
        let mut g = EntangleGraph::new();
        g.add_edge("a", "b", 0.9, EdgeType::Contradiction);

        let edges = g.neighbors("a").unwrap();
        assert_eq!(edges.len(), 1);
        assert_eq!(edges[0].edge_type, EdgeType::Contradiction);
        assert!((edges[0].weight - 0.9).abs() < 1e-6);
    }

    #[test]
    fn test_contradiction_spread() {
        let mut g = EntangleGraph::new();
        // Normal associative edge
        g.add_edge("a", "b", 0.8, EdgeType::Semantic);
        // Contradiction edge from b to c — inhibits c
        g.add_edge("b", "c", 0.7, EdgeType::Contradiction);

        let r = g.spread("a", 2, 10);
        assert_eq!(r.len(), 2);
        // b should have positive accumulated_weight (0.8)
        assert_eq!(r[0].id, "b");
        assert!((r[0].accumulated_weight - 0.8).abs() < 1e-6);
        // c should have negative accumulated_weight (0.8 * -0.5 = -0.4)
        assert_eq!(r[1].id, "c");
        assert!((r[1].accumulated_weight - (-0.4)).abs() < 1e-6);
    }

    #[test]
    fn test_contradiction_pairs_in() {
        let mut g = EntangleGraph::new();
        g.add_edge("a", "b", 0.8, EdgeType::Contradiction);
        g.add_edge("a", "c", 0.6, EdgeType::Semantic);
        g.add_edge("b", "d", 0.7, EdgeType::Contradiction);
        g.add_edge("d", "a", 0.5, EdgeType::Contradiction);

        let ids = vec!["a", "b", "c"];
        let pairs = g.contradiction_pairs_in(&ids);
        // Expected: (a, b) — both a and b are in ids, edge a->b is Contradiction
        // b->d not included because d is not in ids
        // d->a not included because d is not in ids
        assert_eq!(pairs.len(), 1);
        assert_eq!(pairs[0], ("a", "b"));
    }

    #[test]
    fn test_decay_edge() {
        let mut g = EntangleGraph::new();
        g.add_edge("a", "b", 0.8, EdgeType::Semantic);

        // Decay with a small number of hours — weight should decrease but stay
        // above threshold.
        let survived = g.decay_edge("a", "b", 1.0);
        assert!(survived, "edge should survive 1 hour of decay");
        let edges = g.neighbors("a").unwrap();
        let expected = 0.8_f32 * (-0.05_f64 * 1.0_f64.sqrt()).exp() as f32;
        assert!(
            (edges[0].weight - expected).abs() < 1e-6,
            "expected {expected}, got {}",
            edges[0].weight
        );

        // Decay repeatedly until the edge falls below EDGE_PRUNE_THRESHOLD.
        // With lambda = exp(-0.05), after enough hours it drops below 0.03:
        // starting from 0.8, decay for 5000 hours -> weight becomes ~0.8 * exp(-0.05*sqrt(5000))
        // = 0.8 * exp(-0.05*70.71) = 0.8 * exp(-3.535) = 0.8 * 0.029 = 0.023 < 0.03
        let mut g2 = EntangleGraph::new();
        g2.add_edge("a", "b", 0.8, EdgeType::Semantic);
        let survived = g2.decay_edge("a", "b", 5000.0);
        assert!(!survived, "edge should be removed after heavy decay");
        assert!(g2.neighbors("a").unwrap().is_empty());
    }

    #[test]
    fn test_clamp_degree() {
        // Create a graph with 3 nodes and 60 edges each => 180 total edges,
        // avg degree = 180 / 3 = 60 > MAX_AVG_DEGREE (30).
        let mut g = EntangleGraph::new();
        for i in 0..3 {
            let from = format!("n{i}");
            for j in 0..60 {
                let to = format!("m{j}");
                g.add_edge(&from, &to, 0.5, EdgeType::Semantic);
            }
        }

        assert_eq!(g.node_count(), 63); // 3 + 60
        assert_eq!(g.edge_count(), 180);

        let removed = g.clamp_avg_degree();
        assert!(removed > 0, "some edges should be removed");

        let n = g.node_count();
        let e = g.edge_count();
        let avg_deg = e as f64 / n.max(1) as f64;
        assert!(
            avg_deg <= (MAX_AVG_DEGREE as f64) * 1.1,
            "avg degree {avg_deg} should be close to MAX_AVG_DEGREE {MAX_AVG_DEGREE}"
        );
    }
}
