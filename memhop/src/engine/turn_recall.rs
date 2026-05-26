//! turn_recall — Agent per-turn retrieval fusing L0 Cortex + L1 Hopfield + L1 EntangleGraph.

use std::collections::HashSet;

use rayon::join;

use crate::engine::helpers::{f16_to_f32, now_millis};
use crate::engine::EngineInner;
use crate::error::Result;
use crate::types::{Memory, TurnContext, TurnRecallPath, TurnRecallResult};

impl EngineInner {
    /// Agent per-turn retrieval — fuses three memory layers.
    ///
    /// Pipeline:
    ///   1. **Scene gating**: O(1) fingerprint match narrows the candidate set to a
    ///      session (Layer 1) or knowledge-tree node (Layer 2) when applicable.
    ///   2. **L0 Cortex**: zero-latency fetch of the most recent N entries for the session.
    ///   3. **L1 Hopfield**: one-step associative recall of the query against either the
    ///      scene-gated candidates or the full pattern pool.
    ///   4. **L1 EntangleGraph**: if the Hopfield winner exists, BFS spreading from it.
    ///   5. **Dedup**: recent IDs are excluded from associative and related results.
    ///   6. **Scene anchor maintenance**: anchor on success, count misses on failure.
    pub fn turn_recall(&self, ctx: &TurnContext) -> Result<TurnRecallResult> {
        self.check_closed()?;
        let start = std::time::Instant::now();

        // Resolve tree reference upfront (needed by Hopfield path).
        let tree_name = ctx
            .scope
            .as_ref()
            .and_then(|s| s.trees.as_ref())
            .and_then(|t| t.first())
            .map(|s| s.as_str());
        let tree = self.get_tree(tree_name)?;

        // Encode query once — needed by both scene gating and Hopfield recall.
        let encoded = self.encoder.encode_full(&ctx.query);
        let query_f32 = f16_to_f32(&encoded.dense);

        // ── Step 1: Scene gating — narrow candidates via fingerprint match.
        // Layer 1 (session) is preferred; falls back to Layer 2 (tree path)
        // and Layer 3 (active scene anchor) on successive misses.
        let (gated_session, gated_node) = {
            let state = self.scene_state.lock().unwrap_or_else(|e| e.into_inner());

            // Layer 1: session fingerprint matching
            let sess = state.match_session_fingerprint(&query_f32);

            // Layer 2: knowledge tree path prediction (fallback from L1)
            let node = if sess.is_none() {
                state.predict_tree_path(&query_f32)
            } else {
                None
            };

            // Layer 3: implicit scene anchoring (fallback from L1 + L2)
            let fallback = if sess.is_none() && node.is_none() {
                state.active_scene.as_ref()
                    .filter(|scene| scene.miss_count < 3)
                    .and_then(|scene| scene.session_id.clone())
            } else {
                None
            };

            (sess.or(fallback), node)
        };

        let gated_candidates: Vec<String> = if let Some(sid) = &gated_session {
            tree.meta_index
                .session_memory_ids(sid)
                .map(|set| set.iter().cloned().collect())
                .unwrap_or_default()
        } else if let Some(nid) = &gated_node {
            tree.meta_index
                .by_parent
                .get(nid)
                .map(|set| set.iter().cloned().collect())
                .unwrap_or_default()
        } else {
            Vec::new()
        };
        let gate_applied = !gated_candidates.is_empty();

        let query_ref = &query_f32;
        let gated_ref = &gated_candidates;

        // ── Steps 2+3: Cortex and Hopfield run in parallel ───────
        let (recent, hopfield_hit) = join(
            || -> Vec<Memory> {
                self.cortex
                    .recent(&ctx.session_id, ctx.recent_limit)
                    .into_iter()
                    .cloned()
                    .collect()
            },
            || -> Option<(String, f32)> {
                if !gated_ref.is_empty() {
                    let refs: Vec<&str> = gated_ref.iter().map(|s| s.as_str()).collect();
                    tree.hopfield
                        .recall_among_topk(query_ref, &refs, 1)
                        .into_iter()
                        .next()
                } else {
                    tree.hopfield.recall(query_ref)
                }
            },
        );
        let cortex_count = recent.len();

        // Build dedup set from recent IDs.
        let recent_ids: HashSet<&str> = recent.iter().map(|m| m.id.as_str()).collect();

        let (associative, hopfield_confidence) = match hopfield_hit {
            Some((ref id, conf)) if conf >= self.confidence_threshold => {
                if recent_ids.contains(id.as_str()) {
                    // Winner already in recent — skip to avoid duplication.
                    (None, Some(conf))
                } else {
                    let mem = self.build_memory(tree, id, conf)?;
                    (mem, Some(conf))
                }
            }
            Some((_, conf)) => (None, Some(conf)),
            None => (None, None),
        };

        // ── Step 4: L1 EntangleGraph — spreading activation ──────
        let mut spread_seed_id: Option<String> = None;
        let mut spread_results_count: usize = 0;

        let related = if let Some(ref assoc) = associative {
            spread_seed_id = Some(assoc.id.clone());

            // Check spread cache first (TTL-based, invalidated on mutations).
            let spread = if let Some(cached) = self.spread_cache.get(&assoc.id) {
                cached.clone()
            } else {
                self.entangle_graph
                    .spread(&assoc.id, ctx.spread_depth, ctx.spread_cap)
            };
            spread_results_count = spread.len();

            let mut related_mems = Vec::new();
            for sr in &spread {
                // Skip if already in recent or is the associative result itself.
                if recent_ids.contains(sr.id.as_str()) {
                    continue;
                }
                if let Ok(Some(mem)) = self.load_memory_by_id(&sr.id) {
                    related_mems.push(mem);
                }
            }
            related_mems
        } else {
            Vec::new()
        };

        // ── Step 6: Scene anchor maintenance + rolling turn summary update.
        // When the gate fired and Hopfield returned a confident winner, anchor the
        // scene for follow-up turns. Otherwise count a miss; three consecutive misses
        // clear the anchor so the next turn can re-route freely.
        {
            let mut state = self.scene_state.lock().unwrap_or_else(|e| e.into_inner());
            state.update_recent_turns(&encoded.dense);
            if gate_applied {
                match &hopfield_hit {
                    Some((_, conf)) if *conf >= self.confidence_threshold => {
                        state.anchor_scene(&ctx.session_id, *conf as f64, now_millis());
                    }
                    _ => {
                        if state.record_miss() {
                            state.reset_scene();
                        }
                    }
                }
            }
        }

        let latency_us = start.elapsed().as_micros() as u64;

        Ok(TurnRecallResult {
            recent,
            associative,
            related,
            recall_path: TurnRecallPath {
                cortex_count,
                hopfield_confidence,
                spread_seed_id,
                spread_results_count,
            },
            latency_us,
        })
    }

    /// Warm the spread cache after a turn_recall (call from mutable context).
    #[allow(dead_code)]
    pub fn warm_spread_cache(&mut self, seed_id: &str, depth: usize, cap: usize) {
        if self.spread_cache.get(seed_id).is_none() {
            let results = self.entangle_graph.spread(seed_id, depth, cap);
            self.spread_cache.insert(seed_id.to_string(), results);
        }
    }

    /// Load a memory by its full ID, deriving the tree name from the ID prefix.
    fn load_memory_by_id(&self, id: &str) -> Result<Option<Memory>> {
        let tree_name = id
            .find(":m_")
            .map(|p| &id[..p])
            .unwrap_or(&self.default_tree);
        if let Some(tree) = self.trees.get(tree_name) {
            return self.build_memory(tree, id, 0.0);
        }
        Ok(None)
    }
}
