//! Schema extraction and stability management.

#![allow(dead_code)]

use std::collections::HashMap;

use half::f16;

use crate::engram::{Engram, EngramKind, SchemaExtra};
use crate::hopfield::cosine_similarity_f16;

// ── Schema Stability ─────────────────────────────────────────

/// Compute the stability of a schema based on its source episodes,
/// internal consistency, and contradiction penalty.
///
/// Stability = source × consistency × penalty
///   source = sigmoid(3 - n) = 1 / (1 + exp(3 - n))
///   penalty = 1 - min(contradiction_count × 0.1, 0.5)
pub fn schema_stability(schema: &SchemaExtra) -> f32 {
    let n = schema.source_episodes.len() as f32;
    let source = 1.0 / (1.0 + (3.0 - n).exp()); // sigmoid centered at 3
    let penalty = 1.0 - (schema.contradiction_count as f32 * 0.1).min(0.5);

    let stability = source * schema.internal_consistency * penalty;
    stability.clamp(0.0, 1.0)
}

// ── Schema Emergence ─────────────────────────────────────────

/// Attempt to create a schema from a cluster of similar episode engrams.
///
/// If the cluster meets minimum size, a Schema engram is created.
pub fn try_emerge_schema(
    cluster_ids: &[String],
    cluster_engrams: &[&Engram],
    now: i64,
) -> Option<(Engram, SchemaExtra)> {
    if cluster_ids.len() < 3 {
        return None;
    }

    // Compute centroid vector (mean of all episode vectors)
    let dim = cluster_engrams[0].vector.len();
    let mut centroid: Vec<f32> = vec![0.0; dim];
    for e in cluster_engrams.iter() {
        for (i, val) in e.vector.iter().enumerate() {
            centroid[i] += val.to_f32();
        }
    }
    let n = cluster_engrams.len() as f32;
    for v in &mut centroid {
        *v /= n;
    }

    // L2 normalize centroid
    let norm = centroid.iter().map(|v| v * v).sum::<f32>().sqrt();
    let centroid_f16: Vec<f16> = if norm > 0.0 {
        centroid.iter().map(|v| f16::from_f32(v / norm)).collect()
    } else {
        centroid.iter().map(|v| f16::from_f32(*v)).collect()
    };

    // Compute internal consistency: average pairwise cosine similarity
    let mut total_sim = 0.0;
    let mut pairs = 0;
    for i in 0..cluster_engrams.len() {
        for j in (i + 1)..cluster_engrams.len() {
            total_sim += cosine_similarity_f16(&cluster_engrams[i].vector, &cluster_engrams[j].vector);
            pairs += 1;
        }
    }
    let internal_consistency = if pairs > 0 {
        total_sim / pairs as f32
    } else {
        1.0
    };

    // Collect common keywords
    let mut kw_freq: HashMap<String, u32> = HashMap::new();
    for e in cluster_engrams {
        for kw in &e.keywords {
            *kw_freq.entry(kw.clone()).or_insert(0) += 1;
        }
    }
    let mut keywords: Vec<String> = kw_freq
        .into_iter()
        .filter(|(_, count)| *count >= (cluster_engrams.len() as u32 / 2).max(1))
        .map(|(kw, _)| kw)
        .collect();
    keywords.sort();
    keywords.truncate(10); // Max 10 keywords

    let summary = cluster_engrams
        .first()
        .map(|e| e.text.clone())
        .unwrap_or_default();
    let schema_text = format!("Schema from {} episodes: {}", cluster_ids.len(), summary);

    let schema_id = generate_schema_id(cluster_ids);

    let mut schema_extra = SchemaExtra::new(
        cluster_ids.to_vec(),
        centroid_f16,
    );
    schema_extra.match_count = cluster_ids.len() as u32;
    schema_extra.internal_consistency = internal_consistency;
    schema_extra.stability = schema_stability(&schema_extra);

    let schema_engram = Engram {
        id: schema_id.clone(),
        text: schema_text,
        summary: Some(format!(
            "Schema emerged from {} episodes, stability {:.2}",
            cluster_ids.len(),
            schema_extra.stability
        )),
        vector: schema_extra.centroid_vector.clone(),
        keywords,
        content_type: Some("schema".to_string()),
        valence: cluster_engrams.iter().map(|e| e.valence).sum::<f32>() / n,
        arousal: cluster_engrams.iter().map(|e| e.arousal).sum::<f32>() / n,
        vitality: 1.0,
        protection: crate::engram::Protection::Normal,
        created_at: now,
        last_activated: now,
        activation_count: 0,
        kind: EngramKind::Schema,
        meta: HashMap::new(),
        is_archived: false,
        is_dormant: false,
    };

    Some((schema_engram, schema_extra))
}

/// Generate a deterministic schema ID from a sorted list of source episode IDs.
fn generate_schema_id(episode_ids: &[String]) -> String {
    use std::collections::hash_map::DefaultHasher;
    use std::hash::{Hash, Hasher};

    let mut hasher = DefaultHasher::new();
    for id in episode_ids {
        id.hash(&mut hasher);
    }
    let hash = hasher.finish();
    format!("sc_{:016x}", hash)
}

/// Compute pairwise cosine similarity matrix for a set of vectors.
/// Returns (similarity_matrix, max_similarity, mean_similarity).
pub fn pairwise_similarities(vectors: &[&[f16]]) -> (Vec<Vec<f32>>, f32, f32) {
    let n = vectors.len();
    let mut matrix = vec![vec![0.0; n]; n];
    let mut max_sim = 0.0f32;
    let mut sum_sim = 0.0f32;
    let mut count = 0;

    for i in 0..n {
        matrix[i][i] = 1.0;
        for j in (i + 1)..n {
            let sim = cosine_similarity_f16(vectors[i], vectors[j]);
            matrix[i][j] = sim;
            matrix[j][i] = sim;
            max_sim = max_sim.max(sim);
            sum_sim += sim;
            count += 1;
        }
    }

    let mean_sim = if count > 0 { sum_sim / count as f32 } else { 1.0 };
    (matrix, max_sim, mean_sim)
}

/// Check whether a set of episodes forms a valid schema cluster.
/// Cluster is valid if mean pairwise similarity exceeds threshold.
pub fn is_cluster_valid(vectors: &[&[f16]], threshold: f32) -> bool {
    if vectors.len() < 2 {
        return false;
    }
    let (_, _, mean_sim) = pairwise_similarities(vectors);
    mean_sim >= threshold
}
