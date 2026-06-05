//! organize — memory organization: keyword extraction, node organization, topic boundary detection.
//! Operates on L1 + L2 layers. Stateless -- all state in LMDB.

pub mod reflect;
pub mod plan;

use crate::brain::Brain;
use crate::error::{Result, MemHopError};

// ── Stop words ────────────────────────────────────────────

const STOP_WORDS: &[&str] = &[
    // Chinese
    "的", "了", "在", "是", "我", "有", "和", "就", "不", "人", "都", "一",
    "一个", "上", "也", "很", "到", "说", "要", "去", "你", "会", "着", "没有",
    "看", "好", "自己", "这", "他", "她", "它", "们", "那", "这个", "那个",
    "什么", "怎么", "为什么", "因为", "所以", "但是", "虽然", "如果",
    // English
    "the", "a", "an", "is", "are", "was", "were", "be", "been", "being",
    "have", "has", "had", "do", "does", "did", "will", "would", "could",
    "should", "may", "might", "can", "shall", "to", "of", "in", "for",
    "on", "with", "at", "by", "from", "as", "into", "through", "during",
    "before", "after", "above", "below", "between", "under", "again",
    "further", "then", "once", "here", "there", "when", "where", "why",
    "how", "all", "each", "every", "both", "few", "more", "most", "other",
    "some", "such", "no", "nor", "not", "only", "own", "same", "so",
    "than", "too", "very", "just", "because", "as", "until", "while",
    "about", "over", "after", "before", "between", "under", "again",
    "and", "but", "or", "if", "while", "that", "this", "these", "those",
];

/// Extract keywords from text: length-first sorting, stop word filtered.
pub fn extract_keywords(text: &str, max: usize) -> Vec<String> {
    let mut words: Vec<String> = text
        .split(|c: char| !c.is_alphanumeric())
        .filter(|s| {
            let t = s.trim();
            t.len() >= 2 && !STOP_WORDS.contains(&t)
        })
        .map(|s| s.trim().to_string())
        .collect();

    words.sort_by(|a, b| {
        b.len().cmp(&a.len()).then_with(|| {
            let cnt_b = text.matches(b).count();
            let cnt_a = text.matches(a).count();
            cnt_b.cmp(&cnt_a)
        })
    });
    words.dedup();
    words.truncate(max);
    words
}

/// Organize a stored L1 node: extract keywords and write back.
pub fn organize_node(brain: &mut Brain, node_id: &str) -> Result<()> {
    let txn = brain.l1_env.env.read_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let node = match brain.l1.get_node(&txn, &brain.l1_env, node_id)? {
        Some(n) => n,
        None => return Err(MemHopError::NotFound(format!("node {} not found", node_id))),
    };
    drop(txn);

    let keywords = extract_keywords(&node.text, 10);
    if keywords.is_empty() {
        return Ok(());
    }

    // Update node's keywords and write back
    let mut updated = node.clone();
    updated.keywords = keywords;
    updated.updated_at = chrono::Utc::now().timestamp_millis();
    let bytes = bincode::serialize(&updated)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    let env = brain.l1_env.env.clone();
    let mut wtxn = env.write_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    brain.l1_env.nodes.put(&mut wtxn, node_id, &bytes)
        .map_err(|e| MemHopError::Storage(e.to_string()))?;
    wtxn.commit().map_err(|e| MemHopError::Storage(e.to_string()))?;

    Ok(())
}

/// Detect topic boundary: compare two consecutive L1 nodes' vector cosine similarity.
/// Returns true if vectors differ significantly (sharp drop suggests topic shift).
pub fn detect_topic_boundary(brain: &Brain, node_a: &str, node_b: &str) -> Result<bool> {
    let txn = brain.l1_env.env.read_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;

    let a = match brain.l1.get_node(&txn, &brain.l1_env, node_a)? {
        Some(n) => n,
        None => return Err(MemHopError::NotFound(format!("node {} not found", node_a))),
    };
    let b = match brain.l1.get_node(&txn, &brain.l1_env, node_b)? {
        Some(n) => n,
        None => return Err(MemHopError::NotFound(format!("node {} not found", node_b))),
    };

    if a.vector.is_empty() || b.vector.is_empty() || a.vector.len() != b.vector.len() {
        // Fallback: compare ngram overlap
        let overlap: f32 = a.sparse.keys().filter(|k| b.sparse.contains_key(*k)).count() as f32;
        let total = (a.sparse.len() + b.sparse.len()) as f32;
        let jaccard = if total > 0.0 { overlap / (total - overlap) } else { 0.0 };
        return Ok(jaccard < 0.1);
    }

    let mut dot = 0.0f32;
    let mut norm_a = 0.0f32;
    let mut norm_b = 0.0f32;
    for i in 0..a.vector.len() {
        let va = a.vector[i].to_f32();
        let vb = b.vector[i].to_f32();
        dot += va * vb;
        norm_a += va * va;
        norm_b += vb * vb;
    }

    let cos_sim = dot / (norm_a.sqrt() * norm_b.sqrt() + 1e-8);
    // Cosine < 0.3 suggests a topic shift
    Ok(cos_sim < 0.3)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_keywords_basic() {
        let text = "dog 今天早上吃了 豆浆油条";
        let keywords = extract_keywords(text, 5);
        assert!(!keywords.is_empty());
        // Longest by byte length should rank first
        assert_eq!(keywords[0], "今天早上吃了");
        assert!(keywords.iter().any(|k| k == "豆浆油条"));
        assert!(keywords.iter().any(|k| k == "dog"));
    }

    #[test]
    fn test_extract_keywords_chinese() {
        // Space-separated Chinese compounds
        let text = "机器学习 深度学习 自然语言处理 计算机视觉 强化学习";
        let keywords = extract_keywords(text, 3);
        assert_eq!(keywords.len(), 3);
        // Longest compound should rank first
        assert_eq!(keywords[0], "自然语言处理");
    }
}
