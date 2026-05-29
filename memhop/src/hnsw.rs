//! HNSW index — approximate nearest neighbor search on f16 vectors.
//!
//! Pure Rust implementation of Hierarchical Navigable Small World graphs,
//! designed for MemHop's 1024-dim f16 vectors with cosine similarity.
//!
//! Reference: "Efficient and robust approximate nearest neighbor search using
//! Hierarchical Navigable Small World graphs" (Malkov & Yashunin, 2018).

use half::f16;
use rand::Rng;
use serde::{Deserialize, Serialize};
use std::collections::{BinaryHeap, HashMap, HashSet};

use crate::storage::LmdbStorage;

/// Node identifier type — external caller-provided ID.
pub type NodeId = u64;

// ── Heap wrappers for HNSW search ──────────────────────────────────────

/// Min‑heap element: smaller distance → higher priority.
#[derive(Clone, Debug)]
struct Candidate {
    dist: f32,
    id: NodeId,
}

impl Ord for Candidate {
    fn cmp(&self, other: &Self) -> std::cmp::Ordering {
        // Reversed: smaller dist → greater ordering priority
        other
            .dist
            .partial_cmp(&self.dist)
            .unwrap_or(std::cmp::Ordering::Equal)
    }
}

impl PartialOrd for Candidate {
    fn partial_cmp(&self, other: &Self) -> Option<std::cmp::Ordering> {
        Some(self.cmp(other))
    }
}

impl PartialEq for Candidate {
    fn eq(&self, other: &Self) -> bool {
        self.dist == other.dist
    }
}

impl Eq for Candidate {}

/// Max‑heap element: larger distance → higher priority.
#[derive(Clone, Debug)]
struct Farthest {
    dist: f32,
    id: NodeId,
}

impl Ord for Farthest {
    fn cmp(&self, other: &Self) -> std::cmp::Ordering {
        self.dist
            .partial_cmp(&other.dist)
            .unwrap_or(std::cmp::Ordering::Equal)
    }
}

impl PartialOrd for Farthest {
    fn partial_cmp(&self, other: &Self) -> Option<std::cmp::Ordering> {
        Some(self.cmp(other))
    }
}

impl PartialEq for Farthest {
    fn eq(&self, other: &Self) -> bool {
        self.dist == other.dist
    }
}

impl Eq for Farthest {}

// ── HNSW Index ─────────────────────────────────────────────────────────

/// Hierarchical Navigable Small World index for ANN search on `f16` vectors.
///
/// # Parameters (fixed at construction)
///
/// | Parameter | Default | Meaning |
/// |-----------|---------|---------|
/// | `M`       | 16      | Connections per element per layer |
/// | `M_max`   | 32      | Max connections for upper layers  |
/// | `ef_construction` | 200 | Dynamic candidate list during build |
/// | `ef_search` | 50   | Dynamic candidate list during search |
#[derive(Clone, Serialize, Deserialize)]
pub struct HnswIndex {
    /// All vectors flattened: `vectors[internal_idx * dim .. ][.. dim]`
    vectors: Vec<f16>,
    /// Dimensionality of each vector (typically 1024).
    dim: usize,

    /// Level generation.
    /// `graphs[lvl][internal_idx]` = neighbour list of node at level `lvl`.
    graphs: Vec<Vec<Vec<NodeId>>>,

    /// Internal index → external NodeId mapping.
    node_ids: Vec<NodeId>,
    /// External NodeId → internal index mapping.
    internal_map: HashMap<NodeId, usize>,

    /// Entry point (top‑level node).
    entry_point: Option<NodeId>,

    // ── Parameters ──────────────────────────────────────────────────────
    m:              usize, // M
    m_max:          usize, // M_max
    ef_construction: usize,
    ef_search:       usize,

    /// Number of nodes stored.
    node_count: usize,

    /// Level multiplier `1 / ln(M)`.
    ml: f32,

    /// v0.11.0: Soft-delete tombstone set. Nodes in this set are skipped during search.
    /// Not serialized (rebuild from LMDB config on restart).
    #[serde(skip)]
    pub tombstones: HashSet<u64>,
}

impl HnswIndex {
    /// Create an empty index for `dim`-dimensional vectors.
    pub fn new(dim: usize) -> Self {
        let m = 16usize;
        Self {
            vectors: Vec::new(),
            dim,
            graphs: Vec::new(),
            node_ids: Vec::new(),
            internal_map: HashMap::new(),
            entry_point: None,
            m,
            m_max: 32,
            ef_construction: 200,
            ef_search: 50,
            node_count: 0,
            ml: 1.0 / (m as f32).ln(),
            tombstones: HashSet::new(),
        }
    }

    // ── Public API ──────────────────────────────────────────────────────

    /// Insert a vector identified by `id`.
    ///
    /// If `id` already exists the call is silently ignored.
    pub fn insert(&mut self, id: NodeId, vector: &[f16]) {
        debug_assert_eq!(vector.len(), self.dim, "vector dimension mismatch");

        if self.internal_map.contains_key(&id) {
            return; // already present
        }

        let level = self.random_level();

        // ── Empty index → trivial case ──────────────────────────────────
        if self.node_count == 0 {
            self.add_node(id, vector, level);
            self.entry_point = Some(id);
            return;
        }

        let top_level = self.graphs.len().saturating_sub(1);
        let entry = self
            .entry_point
            .expect("non‑empty index must have an entry point");

        // Convert query to f32 once for all subsequent distance calls.
        let query_f32: Vec<f32> = vector.iter().map(|x| x.to_f32()).collect();

        // ── Traverse from top level down to level + 1 ───────────────────
        // At each of these layers we only need the single closest node to
        // serve as the entry point for the layer below.
        let mut ep = entry;
        for lc in (level + 1..=top_level).rev() {
            let w = self.search_layer(&query_f32, ep, 1, lc);
            if let Some(&(closest, _)) = w.first() {
                ep = closest;
            }
        }

        // ── Register the new node in all levels 0 ..= level ─────────────
        self.add_node(id, vector, level);

        // ── Connect at each layer ───────────────────────────────────────
        for lc in (0..=level.min(top_level)).rev() {
            let w = self.search_layer(&query_f32, ep, self.ef_construction, lc);
            let neighbors = select_neighbors_simple(&w, self.m);

            let idx = self.internal_map[&id];
            for &nb in &neighbors {
                let nb_idx = self.internal_map[&nb];

                // Bidirectional link
                self.graphs[lc][idx].push(nb);
                self.graphs[lc][nb_idx].push(id);

                // Shrink neighbour if it exceeds M_max at this layer
                if self.graphs[lc][nb_idx].len() > self.m_max {
                    self.shrink_connections(lc, nb_idx);
                }
            }

            // Update entry for next (lower) layer
            if let Some(&(closest, _)) = w.first() {
                ep = closest;
            }
        }

        // ── Promote to entry point if at a higher level ─────────────────
        if level > top_level {
            self.entry_point = Some(id);
        }
    }

    /// Search for the `k` nearest neighbours of `query`.
    ///
    /// Returns `Vec<(node_id, cosine_similarity)>` sorted by similarity
    /// (highest first, range [-1, 1]).
    pub fn search(&self, query: &[f16], k: usize) -> Vec<(NodeId, f32)> {
        debug_assert_eq!(query.len(), self.dim, "query dimension mismatch");

        if self.is_empty() {
            return Vec::new();
        }

        let query_f32: Vec<f32> = query.iter().map(|x| x.to_f32()).collect();

        let entry = self
            .entry_point
            .expect("non‑empty index must have an entry point");

        // ── Traverse from top level down to level 1 ─────────────────────
        let top_level = self.graphs.len().saturating_sub(1);
        let mut ep = entry;
        for lc in (1..=top_level).rev() {
            let w = self.search_layer(&query_f32, ep, 1, lc);
            if let Some(&(closest, _)) = w.first() {
                ep = closest;
            }
        }

        // ── Beam search at level 0 ──────────────────────────────────────
        // Use ef = max(ef_search, k) so we have enough candidates.
        let ef = self.ef_search.max(k);
        let w = self.search_layer(&query_f32, ep, ef, 0);

        // Convert distance back to similarity, sort by similarity descending, return top‑k.
        let mut results: Vec<(NodeId, f32)> = w
            .into_iter()
            .map(|(id, dist)| (id, 1.0 - dist))
            .collect();
        results.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));

        // v0.11.0: Filter out tombstoned (soft-deleted) nodes.
        results.retain(|(id, _)| !self.tombstones.contains(id));

        results.truncate(k);
        results
    }

    /// Serialize to bincode bytes.
    pub fn to_bytes(&self) -> std::result::Result<Vec<u8>, Box<dyn std::error::Error>> {
        Ok(bincode::serialize(self)?)
    }

    /// Deserialize from bincode bytes.
    pub fn from_bytes(data: &[u8]) -> std::result::Result<Self, Box<dyn std::error::Error>> {
        Ok(bincode::deserialize(data)?)
    }

    /// Save this index to LMDB storage.
    pub fn save_to_storage(&self, storage: &LmdbStorage) -> Result<(), String> {
        let bytes = self
            .to_bytes()
            .map_err(|e| format!("hnsw serialize: {}", e))?;
        let mut txn = storage
            .begin_write()
            .map_err(|e| format!("storage write txn: {}", e))?;
        storage
            .put_hnsw_index(&mut txn, &bytes)
            .map_err(|e| format!("storage put: {}", e))?;
        txn.commit()
            .map_err(|e| format!("storage commit: {}", e))?;
        Ok(())
    }

    /// Load this index from LMDB storage, returning `None` if no index exists.
    pub fn load_from_storage(storage: &LmdbStorage) -> Result<Option<Self>, String> {
        let txn = storage
            .begin_read()
            .map_err(|e| format!("storage read txn: {}", e))?;
        match storage
            .get_hnsw_index(&txn)
            .map_err(|e| format!("storage get: {}", e))?
        {
            Some(bytes) => {
                let index = Self::from_bytes(&bytes)
                    .map_err(|e| format!("hnsw deserialize: {}", e))?;
                Ok(Some(index))
            }
            None => Ok(None),
        }
    }

    /// Number of stored nodes.
    pub fn len(&self) -> usize {
        self.node_count
    }

    /// Whether the index is empty.
    pub fn is_empty(&self) -> bool {
        self.node_count == 0
    }

    // ── v0.11.0: Soft-delete (tombstone) API ────────────────────────────

    /// Soft-delete a node by `node_id`. The node is skipped in search results.
    pub fn mark_deleted(&mut self, node_id: u64) {
        self.tombstones.insert(node_id);
    }

    /// Ratio of tombstoned nodes to total nodes.
    pub fn tombstone_ratio(&self) -> f32 {
        if self.is_empty() {
            return 0.0;
        }
        self.tombstones.len() as f32 / self.len() as f32
    }

    /// Rebuild HNSW index without tombstoned nodes.
    ///
    /// Extracts all non-tombstoned node vectors, clears the index, and
    /// re-inserts them with their original `node_id`s.  Returns the number
    /// of removed nodes.
    pub fn compact(&mut self) -> usize {
        let removed = self.tombstones.len();
        if removed == 0 {
            return 0;
        }

        // Collect active (non-tombstoned) nodes' vectors.
        let mut active_nodes: Vec<(NodeId, Vec<f16>)> = Vec::new();
        for (internal_idx, &node_id) in self.node_ids.iter().enumerate() {
            if !self.tombstones.contains(&node_id) {
                let offset = internal_idx * self.dim;
                let vector = self.vectors[offset..offset + self.dim].to_vec();
                active_nodes.push((node_id, vector));
            }
        }

        // Clear the current index.
        self.vectors.clear();
        self.graphs.clear();
        self.node_ids.clear();
        self.internal_map.clear();
        self.entry_point = None;
        self.node_count = 0;
        self.tombstones.clear();

        // Re-insert all active nodes with their original node_ids.
        for (node_id, vector) in &active_nodes {
            self.insert(*node_id, vector);
        }

        removed
    }

    // ── Internal helpers ────────────────────────────────────────────────

    /// Beam search at a single level `lc`.
    ///
    /// Returns up to `ef` candidates sorted by distance (ascending).
    /// Uses the classic HNSW SEARCH‑LAYER algorithm.
    fn search_layer(
        &self,
        query: &[f32],
        entry: NodeId,
        ef: usize,
        level: usize,
    ) -> Vec<(NodeId, f32)> {
        let mut visited = HashSet::new();
        visited.insert(entry);

        let entry_dist = self.distance(entry, query);
        let mut candidates: BinaryHeap<Candidate> = BinaryHeap::new();
        candidates.push(Candidate {
            dist: entry_dist,
            id: entry,
        });

        let mut results: BinaryHeap<Farthest> = BinaryHeap::new();
        results.push(Farthest {
            dist: entry_dist,
            id: entry,
        });

        while let Some(c) = candidates.pop() {
            // If the closest candidate is farther than the farthest result,
            // no remaining candidate can improve the result set.
            let farthest = results
                .peek()
                .expect("results never empty while candidates exist");
            if c.dist > farthest.dist {
                break;
            }

            let idx = self.internal_map[&c.id];
            for &neighbor in &self.graphs[level][idx] {
                if visited.insert(neighbor) {
                    let dist = self.distance(neighbor, query);
                    let farthest = results
                        .peek()
                        .expect("results never empty after insert");

                    if dist < farthest.dist || results.len() < ef {
                        candidates.push(Candidate { dist, id: neighbor });
                        results.push(Farthest { dist, id: neighbor });
                        if results.len() > ef {
                            results.pop(); // discard farthest
                        }
                    }
                }
            }
        }

        // Convert the max‑heap into a Vec sorted by distance (ascending).
        let mut result_vec: Vec<(NodeId, f32)> = results
            .into_iter()
            .map(|r| (r.id, r.dist))
            .collect();
        result_vec.sort_by(|a, b| {
            a.1.partial_cmp(&b.1)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        result_vec
    }

    /// Cosine distance (1 - cosine_similarity) between a stored node and
    /// an f32 query vector.  Range [0, 2].
    fn distance(&self, node: NodeId, query: &[f32]) -> f32 {
        let idx = self
            .internal_map
            .get(&node)
            .unwrap_or_else(|| panic!("HnswIndex::distance: unknown node {:?}", node));
        let offset = idx * self.dim;
        let stored = &self.vectors[offset..offset + self.dim];
        1.0 - cosine_similarity(stored, query)
    }

    /// Greedy traversal at a given level.
    ///
    /// Repeatedly moves to the closest neighbour until no improvement,
    /// returns the final closest node and its distance.
    #[allow(dead_code)]
    fn greedy_search(&self, query: &[f32], level: usize, entry: NodeId) -> (NodeId, f32) {
        let mut current = entry;
        let mut best_dist = self.distance(current, query);

        loop {
            let mut improved = false;
            let idx = self.internal_map[&current];
            for &neighbor in &self.graphs[level][idx] {
                let dist = self.distance(neighbor, query);
                if dist < best_dist {
                    best_dist = dist;
                    current = neighbor;
                    improved = true;
                }
            }
            if !improved {
                break;
            }
        }

        (current, best_dist)
    }

    /// Shrink the neighbour list of `internal_idx` at `level` to `M_max`.
    fn shrink_connections(&mut self, level: usize, idx: usize) {
        // Build f32 query from the node's own stored vector.
        let offset = idx * self.dim;
        let stored = &self.vectors[offset..offset + self.dim];
        let query_f32: Vec<f32> = stored.iter().map(|x| x.to_f32()).collect();

        let old_neighbors = std::mem::take(&mut self.graphs[level][idx]);
        let mut scored: Vec<(NodeId, f32)> = old_neighbors
            .iter()
            .map(|&n| (n, self.distance(n, &query_f32)))
            .collect();
        scored.sort_by(|a, b| {
            a.1.partial_cmp(&b.1)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        scored.truncate(self.m_max);

        self.graphs[level][idx] = scored.into_iter().map(|(id, _)| id).collect();
    }

    /// Register a new node and allocate its graph entries.
    fn add_node(&mut self, id: NodeId, vector: &[f16], level: usize) {
        let idx = self.node_count;
        self.node_ids.push(id);
        self.internal_map.insert(id, idx);

        // Append vector to flattened storage.
        self.vectors.extend_from_slice(vector);

        // Create new levels (up to `level`) if they don't exist yet.
        // Each new level gets an entry for EVERY existing node (empty Vec).
        while self.graphs.len() <= level {
            let mut new_lvl = Vec::with_capacity(self.node_count);
            for _ in 0..self.node_count {
                new_lvl.push(Vec::new());
            }
            self.graphs.push(new_lvl);
        }

        // Append an empty neighbour list for the new node at EVERY existing level.
        // Must cover ALL levels (not just 0..=level) because HNSW entry-point
        // traversal descends through higher levels and needs the node's adjacency
        // list to be present even at levels the node doesn't actively connect at.
        // At levels > level the adjacency list stays empty (no connections).
        for l in 0..self.graphs.len() {
            self.graphs[l].push(Vec::new());
        }

        self.node_count += 1;
    }

    /// Generate a random level using the HNSW level distribution:
    /// `floor(-ln(uniform(0,1)) * mL)`, clamped to avoid INFINITY.
    fn random_level(&self) -> usize {
        let mut rng = rand::thread_rng();
        // Avoid exact 0.0 which would give -ln(0) = INFINITY.
        let r: f32 = rng.r#gen::<f32>().max(f32::MIN_POSITIVE);
        (-r.ln() * self.ml).floor() as usize
    }
}

// ── Standalone helpers ─────────────────────────────────────────────────

/// Cosine similarity between an f16 slice and an f32 slice.
///
/// Returns a value in [-1, 1].  If either vector is zero the result is 0.0.
fn cosine_similarity(a: &[f16], b: &[f32]) -> f32 {
    let mut dot = 0.0f32;
    let mut norm_a = 0.0f32;
    let mut norm_b = 0.0f32;

    for (x, y) in a.iter().zip(b.iter()) {
        let xf = x.to_f32();
        dot += xf * y;
        norm_a += xf * xf;
        norm_b += y * y;
    }

    let denom = (norm_a * norm_b).sqrt();
    if denom > 1e-8 {
        dot / denom
    } else {
        0.0
    }
}

/// Select the top-`m` node ids from `candidates` by distance (ascending).
fn select_neighbors_simple(candidates: &[(NodeId, f32)], m: usize) -> Vec<NodeId> {
    let mut sorted: Vec<(NodeId, f32)> = candidates.to_vec();
    sorted.sort_by(|a, b| {
        a.1.partial_cmp(&b.1)
            .unwrap_or(std::cmp::Ordering::Equal)
    });
    sorted.truncate(m);
    sorted.into_iter().map(|(id, _)| id).collect()
}

// ── Tests ──────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use rand::Rng;
    use rand::rngs::StdRng;
    use rand::SeedableRng;

    /// Build `n` random unit-normalised f16 vectors of `dim` dimensions.
    fn random_vectors(rng: &mut StdRng, n: usize, dim: usize) -> Vec<Vec<f16>> {
        let mut vecs = Vec::with_capacity(n);
        for _ in 0..n {
            let mut raw: Vec<f32> = (0..dim).map(|_| rng.r#gen::<f32>() * 2.0 - 1.0).collect();
            // Normalize
            let norm: f32 = raw.iter().map(|x| x * x).sum::<f32>().sqrt();
            if norm > 1e-8 {
                for v in &mut raw {
                    *v /= norm;
                }
            }
            vecs.push(raw.iter().map(|&x| f16::from_f32(x)).collect());
        }
        vecs
    }

    /// Exhaustive (brute-force) search — ground truth.
    fn brute_force(query: &[f16], vectors: &[Vec<f16>]) -> Vec<(usize, f32)> {
        let qf32: Vec<f32> = query.iter().map(|x| x.to_f32()).collect();
        let mut scored: Vec<(usize, f32)> = vectors
            .iter()
            .enumerate()
            .map(|(i, v)| (i, 1.0 - cosine_similarity(v, &qf32)))
            .collect();
        scored.sort_by(|a, b| {
            a.1.partial_cmp(&b.1)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        scored
    }

    // ── test 1: empty index ─────────────────────────────────────────────

    #[test]
    fn test_empty_index() {
        let idx = HnswIndex::new(1024);
        assert!(idx.is_empty());
        assert_eq!(idx.len(), 0);

        let dummy = vec![f16::from_f32(0.0); 1024];
        let results = idx.search(&dummy, 10);
        assert!(results.is_empty());
    }

    // ── test 2: insert & search ─────────────────────────────────────────

    #[test]
    fn test_insert_and_search() {
        let dim = 64; // small dim for fast tests
        let n = 50;
        let mut rng = StdRng::seed_from_u64(42);
        let vectors = random_vectors(&mut rng, n, dim);

        let mut idx = HnswIndex::new(dim);
        for (i, v) in vectors.iter().enumerate() {
            idx.insert(i as u64, v);
        }

        assert_eq!(idx.len(), n);
        assert!(!idx.is_empty());

        // Query every stored vector → top-1 should always be the vector itself.
        let mut recall_1 = 0usize;
        for (i, v) in vectors.iter().enumerate() {
            let results = idx.search(v, 1);
            if !results.is_empty() && results[0].0 == i as u64 {
                recall_1 += 1;
            }
        }
        assert_eq!(recall_1, n, "recall@1 for exact queries must be 1.0");

        // Query random vectors — measure recall@5 vs brute force.
        let queries = random_vectors(&mut rng, 20, dim);
        let mut hits = 0usize;
        let mut total = 0usize;

        for q in &queries {
            let ground_truth = brute_force(q, &vectors);
            let top_5_gt: HashSet<u64> = ground_truth[..5.min(ground_truth.len())]
                .iter()
                .map(|&(i, _)| i as u64)
                .collect();

            let top_5_idx: HashSet<u64> = idx
                .search(q, 5)
                .into_iter()
                .map(|(id, _)| id)
                .collect();

            hits += top_5_gt.intersection(&top_5_idx).count();
            total += top_5_gt.len();
        }

        let recall = hits as f64 / total as f64;
        assert!(
            recall >= 0.85,
            "recall@5 = {:.3} (expected ≥ 0.85)",
            recall
        );
    }

    // ── test 3: serialization round-trip ────────────────────────────────

    #[test]
    fn test_serialization_roundtrip() {
        let dim = 32;
        let n = 20;
        let mut rng = StdRng::seed_from_u64(123);
        let vectors = random_vectors(&mut rng, n, dim);

        let mut idx = HnswIndex::new(dim);
        for (i, v) in vectors.iter().enumerate() {
            idx.insert(i as u64, v);
        }

        // Serialize
        let bytes = idx.to_bytes().expect("serialization must succeed");
        assert!(!bytes.is_empty());

        // Deserialize
        let restored = HnswIndex::from_bytes(&bytes).expect("deserialization must succeed");
        assert_eq!(restored.len(), idx.len());

        // Verify search results match
        let queries = random_vectors(&mut rng, 10, dim);
        for q in &queries {
            let orig_results = idx.search(q, 5);
            let restored_results = restored.search(q, 5);

            assert_eq!(orig_results.len(), restored_results.len());
            for (o, r) in orig_results.iter().zip(restored_results.iter()) {
                assert_eq!(o.0, r.0, "node id mismatch after round-trip");
                // similarity should be very close
                assert!(
                    (o.1 - r.1).abs() < 1e-5,
                    "similarity mismatch: {} vs {}",
                    o.1,
                    r.1
                );
            }
        }
    }

    // ── test 4: cosine distance bounds ──────────────────────────────────

    #[test]
    fn test_cosine_distance() {
        // Identical vectors → similarity = 1.0, distance = 0.0
        let a_f16: Vec<f16> = (0..16).map(|i| f16::from_f32(i as f32 / 16.0)).collect();
        let a_f32: Vec<f32> = a_f16.iter().map(|x| x.to_f32()).collect();
        let sim = cosine_similarity(&a_f16, &a_f32);
        assert!((sim - 1.0).abs() < 1e-5, "self-similarity must be 1, got {}", sim);

        // Orthogonal vectors → similarity close to 0.0
        let mut b_f16 = vec![f16::from_f32(0.0); 16];
        b_f16[0] = f16::from_f32(1.0);

        let mut c_f16 = vec![f16::from_f32(0.0); 16];
        c_f16[1] = f16::from_f32(1.0);
        let c_f32 = vec![
            0.0, 1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0,
            0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0,
        ];

        let sim_ortho = cosine_similarity(&b_f16, &c_f32);
        assert!(
            sim_ortho.abs() < 1e-6,
            "orthogonal similarity must be ≈0, got {}",
            sim_ortho
        );

        // Opposite vectors → similarity = -1.0, distance = 2.0
        let neg_c_f32: Vec<f32> = c_f32.iter().map(|x| -x).collect();
        let sim_opposite = cosine_similarity(&c_f16, &neg_c_f32);
        assert!(
            (sim_opposite + 1.0).abs() < 1e-5,
            "opposite similarity must be -1, got {}",
            sim_opposite
        );

        // Zero vector → similarity = 0.0 (handled gracefully)
        let zero_f16 = vec![f16::from_f32(0.0); 16];
        let zero_f32 = vec![0.0f32; 16];
        let sim_zero = cosine_similarity(&zero_f16, &zero_f32);
        assert_eq!(sim_zero, 0.0, "zero vector similarity must be 0");
    }
}
