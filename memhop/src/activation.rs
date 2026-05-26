//! Activation spread algorithms — competitive diffusion, emotional alignment, contradiction inhibition.

use std::collections::{HashMap, HashSet};

use crate::engram::Engram;
use crate::personality::Personality;
use crate::unified_graph::UnifiedGraph;

// ── Competitive Spread Activation ─────────────────────────────

/// Result of a competitive spread pass.
pub struct SpreadResult {
    /// Activated engram IDs with their activation scores.
    pub activated: Vec<(String, f32)>,
    /// Trace of the three spread steps.
    #[allow(dead_code)]
    pub steps: SpreadTrace,
}

#[allow(dead_code)]
pub struct SpreadTrace {
    pub seeds: usize,
    pub step1_count: usize,
    pub step2_count: usize,
    pub step3_count: usize,
    pub inhibited: usize,
}

/// Perform competitive diffusion activation over the graph.
///
/// Algorithm (3-pass, top-K truncation, lateral inhibition):
///   1. Seed: start with `seed_ids`, each with activation 1.0.
///      For each seed, spread to its neighbors via edge weight.
///      Aggregate activation per neighbor (sum of seed_activation × edge_weight).
///      Truncate to top-K.
///   2. Repeat from result of step 1: spread to two-hop neighbors.
///   3. Repeat from result of step 2: spread to three-hop neighbors.
///   4. Lateral inhibition: for contradiction edges among activated set,
///      apply winner-take-all: lower-activation ID is suppressed.
///   5. Normalize final activations to [0, 1].
pub fn competitive_spread(
    graph: &UnifiedGraph,
    seeds: &HashMap<String, f32>,
    personality: &Personality,
    top_k: usize,
) -> SpreadResult {
    let top_k = top_k.min(personality.spread_top_k());
    let mut activation: HashMap<String, f32> = seeds.clone();

    let step1 = spread_step(graph, &activation, top_k);
    let step1_len = step1.len();
    activation.extend(step1);

    // Step 2: spread to two-hop
    let step2 = spread_step(graph, &activation, top_k);
    let step2_len = step2.len();
    activation.extend(step2);

    // Step 3: spread to three-hop
    let step3 = spread_step(graph, &activation, top_k);
    let step3_len = step3.len();
    activation.extend(step3);

    let pre_inhibit_count = activation.len();

    // Step 4: Lateral inhibition — contradiction edges cause winner-take-all
    let _inhibited = apply_contradiction_inhibition(graph, &mut activation, personality);

    // Step 5: Normalize
    normalize_activations(&mut activation);

    let ids: Vec<String> = activation.keys().cloned().collect();
    let mut activated: Vec<(String, f32)> = ids
        .into_iter()
        .map(|id| {
            let score = activation[&id];
            (id, score)
        })
        .collect();
    activated.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));

    SpreadResult {
        activated,
        steps: SpreadTrace {
            seeds: seeds.len(),
            step1_count: step1_len,
            step2_count: step2_len,
            step3_count: step3_len,
            inhibited: pre_inhibit_count.saturating_sub(activation.len()),
        },
    }
}

/// One step of spread: from current active set to their neighbors.
fn spread_step(
    graph: &UnifiedGraph,
    current: &HashMap<String, f32>,
    top_k: usize,
) -> HashMap<String, f32> {
    let mut accum: HashMap<String, f32> = HashMap::new();
    for (id, act) in current {
        for edge in graph.edges_of(id) {
            let neighbor_act = *act * edge.weight;
            let entry = accum.entry(edge.target_id.clone()).or_insert(0.0);
            *entry += neighbor_act;
        }
    }

    // Remove self (already in current)
    for id in current.keys() {
        accum.remove(id);
    }

    // Truncate to top-K
    if accum.len() > top_k {
        let mut sorted: Vec<(String, f32)> = accum.into_iter().collect();
        sorted.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(std::cmp::Ordering::Equal));
        sorted.truncate(top_k);
        sorted.into_iter().collect()
    } else {
        accum
    }
}

// ── Contradiction Inhibition ─────────────────────────────────

/// Apply winner-take-all for contradiction edges: lower-activation ID gets suppressed.
fn apply_contradiction_inhibition(
    graph: &UnifiedGraph,
    activation: &mut HashMap<String, f32>,
    personality: &Personality,
) -> usize {
    let ids: HashSet<String> = activation.keys().cloned().collect();
    let contradiction_pairs = graph.contradiction_pairs_in(&ids);
    let mut to_remove = HashSet::new();
    let inhibition_strength = personality.contradiction_inhibition();

    for (a, b) in &contradiction_pairs {
        let act_a = activation.get(a).copied().unwrap_or(0.0);
        let act_b = activation.get(b).copied().unwrap_or(0.0);
        if (act_a - act_b).abs() < 0.1 {
            // Close enough: suppress both slightly
            if let Some(v) = activation.get_mut(a) {
                *v *= 1.0 - inhibition_strength * 0.5;
            }
            if let Some(v) = activation.get_mut(b) {
                *v *= 1.0 - inhibition_strength * 0.5;
            }
        } else if act_a > act_b {
            to_remove.insert(b.clone());
        } else {
            to_remove.insert(a.clone());
        }
    }

    for id in &to_remove {
        activation.remove(id);
    }
    to_remove.len()
}

// ── Emotional Alignment ──────────────────────────────────────

/// Compute emotional alignment between a target emotional state and a memory engram.
/// Returns a multiplier in [0, 1] that can be used to adjust recall scores.
///
/// Alignment is high when valence signs match and arousal levels are close.
pub fn emotional_alignment(target_valence: f32, target_arousal: f32, engram: &Engram) -> f32 {
    // Valence alignment: product of signs (both positive or both negative → positive)
    let valence_align = (target_valence * engram.valence).max(0.0);
    // Arousal closeness: 1 - |diff|
    let arousal_close = 1.0 - (target_arousal - engram.arousal).abs();
    // Combine: valence alignment dominates, arousal modulates
    0.7 * valence_align + 0.3 * arousal_close.clamp(0.0, 1.0)
}

// ── Normalize ────────────────────────────────────────────────

fn normalize_activations(activation: &mut HashMap<String, f32>) {
    let max_val = activation
        .values()
        .cloned()
        .fold(0.0f32, |a, b| a.max(b));
    if max_val > 0.0 {
        for v in activation.values_mut() {
            *v /= max_val;
        }
    }
}
