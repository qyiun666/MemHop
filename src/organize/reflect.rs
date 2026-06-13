use crate::index::sparse::SparseIndex;
use crate::slot::topic::TopicSlot;
use crate::MemHopError;
use std::collections::HashMap;

/// 对指定 Topic 进行反思，生成摘要
///
/// # 参数
/// * `topic_id` - Topic ID 字符串
/// * `data` - mmap 数据切片
/// * `btree` - B树索引
/// * `sparse_index` - 稀疏索引（用于提取关键词）
///
/// # 返回
/// 生成的摘要字符串，如果 Topic 已有 LLM 生成的摘要则返回 None
pub fn reflect_topic(
    topic_id: &str,
    data: &[u8],
    btree: &crate::index::btree::BTreeIndex,
    _sparse_index: &SparseIndex,
) -> Result<Option<String>, MemHopError> {
    use crate::file::page::decode_page_ref;
    use crate::slot::engram::EngramSlot;
    use crate::util::{hash_id, PAGE_SIZE};

    let id_hash = hash_id(topic_id);

    // 加载 Topic
    if let Some(page_ref) = btree.search(id_hash) {
        let (page_id, _slot_index) = decode_page_ref(page_ref);
        let offset = (page_id as usize) * PAGE_SIZE + 32;

        if offset + 69 <= data.len() {
            let topic = TopicSlot::deserialize(&data[offset..])?;

            // 如果已有 LLM 生成的摘要，不覆盖
            if topic.summary.is_some() && !topic.summary.as_ref().unwrap().is_empty() {
                return Ok(None);
            }

            // 聚合所有 Engram 的 sparse 向量，提取 top-10 关键词
            let mut keyword_freq: HashMap<String, u32> = HashMap::new();

            for &node_id in &topic.node_ids {
                if let Some(node_page_ref) = btree.search(node_id) {
                    let (node_page_id, _slot_index) = decode_page_ref(node_page_ref);
                    let node_offset = (node_page_id as usize) * PAGE_SIZE + 32;

                    if node_offset + 128 <= data.len() {
                        if let Ok(engram) = EngramSlot::deserialize(&data[node_offset..]) {
                            // 从 engram 的 keywords 字段收集
                            for keyword in &engram.keywords {
                                *keyword_freq.entry(keyword.clone()).or_insert(0) += 1;
                            }

                            // 也可以从文本中提取关键词
                            let extracted = crate::organize::extract_keywords(&engram.text, 5);
                            for kw in extracted {
                                *keyword_freq.entry(kw).or_insert(0) += 1;
                            }
                        }
                    }
                }
            }

            // 按频率排序，取 top-10
            let mut sorted_keywords: Vec<(String, u32)> = keyword_freq.into_iter().collect();
            sorted_keywords.sort_by_key(|b| std::cmp::Reverse(b.1));

            let top_keywords: Vec<String> = sorted_keywords
                .iter()
                .take(10)
                .map(|(kw, _)| kw.clone())
                .collect();

            // 生成简单摘要（用逗号连接关键词）
            let summary = top_keywords.join(", ");

            return Ok(Some(summary));
        }
    }

    Err(MemHopError::PageNotFound(id_hash as u32))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::file::page::encode_page_ref;
    use crate::index::btree::BTreeIndex;
    use crate::slot::topic::TopicSlot;
    use crate::util::{hash_id, PAGE_SIZE};

    #[test]
    fn test_reflect_topic_with_existing_summary() {
        // 测试已有摘要的 Topic 不会被覆盖
        let topic = TopicSlot {
            id_hash: 12345,
            title: "Test Topic".to_string(),
            summary: Some("Existing summary".to_string()),
            node_ids: vec![1, 2],
            l3_refs: vec![], l4_refs: vec![], parent_id: None,
            created_at: 0,
            updated_at: 0,
            version: 0,
            importance: 0.5,
            activation_score: 0.5,
            is_active: true,
            activation_state: crate::slot::topic::ActivationState::Active,
            centroid_vector: None,
            domain_weights: vec![],
            dialogue_range: (0, 0),
            reserved: [0; 16],
        };

        let topic_data = topic.serialize().unwrap();
        let mut data = vec![0u8; PAGE_SIZE * 2];
        data[32..32 + topic_data.len()].copy_from_slice(&topic_data);

        let mut btree = BTreeIndex::new();
        let page_ref = encode_page_ref(0, 0);
        // 使用与 topic_id 对应的 hash 值
        btree.insert(hash_id("test"), page_ref);

        let sparse_index = SparseIndex::new();

        let result = reflect_topic("test", &data, &btree, &sparse_index).unwrap();
        assert!(result.is_none());
    }

    #[test]
    fn test_reflect_topic_not_found() {
        let btree = BTreeIndex::new();
        let sparse_index = SparseIndex::new();
        let data = vec![0u8; PAGE_SIZE];

        let result = reflect_topic("nonexistent", &data, &btree, &sparse_index);
        assert!(result.is_err());
    }
}
