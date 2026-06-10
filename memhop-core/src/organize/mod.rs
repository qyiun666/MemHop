//! organize — memory organization: keyword extraction, node organization, topic boundary detection.
//! Operates on L1 + L2 layers. Stateless -- all state in redb.

pub mod plan;
pub mod reflect;

use crate::brain::Brain;
use crate::error::Result;

// ── Stop words ────────────────────────────────────────────

const STOP_WORDS: &[&str] = &[
    // Chinese
    "的",
    "了",
    "在",
    "是",
    "我",
    "有",
    "和",
    "就",
    "不",
    "人",
    "都",
    "一",
    "一个",
    "上",
    "也",
    "很",
    "到",
    "说",
    "要",
    "去",
    "你",
    "会",
    "着",
    "没有",
    "看",
    "好",
    "自己",
    "这",
    "他",
    "她",
    "它",
    "们",
    "那",
    "这个",
    "那个",
    "什么",
    "怎么",
    "为什么",
    "因为",
    "所以",
    "但是",
    "虽然",
    "如果",
    // English
    "the",
    "a",
    "an",
    "is",
    "are",
    "was",
    "were",
    "be",
    "been",
    "being",
    "have",
    "has",
    "had",
    "do",
    "does",
    "did",
    "will",
    "would",
    "could",
    "should",
    "may",
    "might",
    "can",
    "shall",
    "to",
    "of",
    "in",
    "for",
    "on",
    "with",
    "at",
    "by",
    "from",
    "as",
    "into",
    "through",
    "during",
    "before",
    "after",
    "above",
    "below",
    "between",
    "under",
    "again",
    "further",
    "then",
    "once",
    "here",
    "there",
    "when",
    "where",
    "why",
    "how",
    "all",
    "each",
    "every",
    "both",
    "few",
    "more",
    "most",
    "other",
    "some",
    "such",
    "no",
    "nor",
    "not",
    "only",
    "own",
    "same",
    "so",
    "than",
    "too",
    "very",
    "just",
    "because",
    "until",
    "while",
    "about",
    "over",
    "and",
    "but",
    "or",
    "if",
    "that",
    "this",
    "these",
    "those",
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
    brain.ensure_l1()?;
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| crate::error::MemHopError::Storage("redb not available".into()))?;
    let rtxn = store.begin_read()?;
    let table = rtxn.open_table(crate::storage::L1_NODES)
        .map_err(|e| crate::error::MemHopError::Storage(format!("open L1_NODES: {}", e)))?;
    let node: crate::engram::KnowledgeNode = match table.get(node_id)
        .map_err(|e| crate::error::MemHopError::Storage(format!("get node: {}", e)))?
    {
        Some(bytes) => bincode::deserialize(bytes.value())
            .map_err(|e| crate::error::MemHopError::Internal(format!("deserialize: {}", e)))?,
        None => return Err(crate::error::MemHopError::NotFound(format!("node {} not found", node_id))),
    };
    drop(table);
    drop(rtxn);

    let keywords = extract_keywords(&node.text, 10);
    if keywords.is_empty() {
        return Ok(());
    }

    // Update node's keywords and write back
    let mut updated = node.clone();
    updated.keywords = keywords;
    updated.updated_at = chrono::Utc::now().timestamp_millis();
    let bytes = bincode::serialize(&updated)?;
    let wtxn = store.begin_write()?;
    {
        let mut table = wtxn.open_table(crate::storage::L1_NODES)
            .map_err(|e| crate::error::MemHopError::Storage(format!("open L1_NODES: {}", e)))?;
        table.insert(node_id, bytes.as_slice())
            .map_err(|e| crate::error::MemHopError::Storage(format!("insert node: {}", e)))?;
    }
    wtxn.commit()
        .map_err(|e| crate::error::MemHopError::Storage(format!("commit: {}", e)))?;

    Ok(())
}

/// Detect topic boundary: compare two consecutive L1 nodes' vector cosine similarity.
/// Returns true if vectors differ significantly (sharp drop suggests topic shift).
pub fn detect_topic_boundary(brain: &mut Brain, node_a: &str, node_b: &str) -> Result<bool> {
    brain.ensure_l1()?;
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| crate::error::MemHopError::Storage("redb not available".into()))?;
    let rtxn = store.begin_read()?;
    let table = rtxn.open_table(crate::storage::L1_NODES)
        .map_err(|e| crate::error::MemHopError::Storage(format!("open L1_NODES: {}", e)))?;

    let read_node = |id: &str| -> Result<crate::engram::KnowledgeNode> {
        match table.get(id)
            .map_err(|e| crate::error::MemHopError::Storage(format!("get node: {}", e)))?
        {
            Some(bytes) => Ok(bincode::deserialize(bytes.value())
                .map_err(|e| crate::error::MemHopError::Internal(format!("deserialize: {}", e)))?),
            None => Err(crate::error::MemHopError::NotFound(format!("node {} not found", id))),
        }
    };

    let a = read_node(node_a)?;
    let b = read_node(node_b)?;
    drop(table);
    drop(rtxn);

    if a.vector.is_empty() || b.vector.is_empty() || a.vector.len() != b.vector.len() {
        // Fallback: compare ngram overlap
        let overlap: f32 = a
            .sparse
            .keys()
            .filter(|k| b.sparse.contains_key(*k))
            .count() as f32;
        let total = (a.sparse.len() + b.sparse.len()) as f32;
        let jaccard = if total > 0.0 {
            overlap / (total - overlap)
        } else {
            0.0
        };
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
