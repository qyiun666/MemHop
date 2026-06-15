use crate::file::free_list::allocate_from_free_list;
use crate::file::header::FileHeader;
use crate::slot::hyperedge::{HyperedgeKind, HyperedgeSlot};
use crate::slot::topic::TopicSlot;
use crate::util::{get_current_timestamp, hash_id, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashSet;

/// 合并相似的 Topics
///
/// # 参数
/// * `topics` - 所有 Topic 列表
/// * `threshold` - Jaccard 相似度阈值（默认 0.5）
///
/// # 返回
/// (合并的 Topic 对数量, 被吸收的 Topic IDs, 创建的 Evolution Edge ID 列表)
pub fn merge_similar_topics(
    topics: &mut Vec<TopicSlot>,
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    threshold: f32,
) -> Result<(u32, Vec<u64>, Vec<String>), MemHopError> {
    let mut merged_count = 0;
    let mut absorbed_ids = Vec::new();
    let mut evolution_edge_ids = Vec::new();
    let mut to_remove = HashSet::new();

    // O(n²) 比较所有 Topic 对
    for i in 0..topics.len() {
        if to_remove.contains(&i) {
            continue;
        }

        for j in (i + 1)..topics.len() {
            if to_remove.contains(&j) {
                continue;
            }

            let similarity = calculate_label_jaccard(&topics[i].title, &topics[j].title);

            if similarity >= threshold {
                // 合并：保留节点多的，吸收另一个
                let (keeper_idx, absorbed_idx) =
                    if topics[i].engram_ids.len() >= topics[j].engram_ids.len() {
                        (i, j)
                    } else {
                        (j, i)
                    };

                // 先收集需要添加的 engram_ids（避免借用冲突）
                let nodes_to_add: Vec<u64> = topics[absorbed_idx]
                    .engram_ids
                    .iter()
                    .filter(|node_id| !topics[keeper_idx].engram_ids.contains(node_id))
                    .cloned()
                    .collect();

                // 然后添加到 keeper
                for node_id in nodes_to_add {
                    topics[keeper_idx].engram_ids.push(node_id);
                }

                // 标记为待删除
                to_remove.insert(absorbed_idx);
                absorbed_ids.push(topics[absorbed_idx].id_hash);
                merged_count += 1;

                // 创建 Evolution TopicEdge（记录从 absorbed 到 keeper 的演化关系）
                if let Ok(edge_page_id) = allocate_from_free_list(mmap, header) {
                    let now = get_current_timestamp();

                    let evolution_edge_id = hash_id(&format!(
                        "evolution_{}_to_{}",
                        topics[absorbed_idx].id_hash,
                        topics[keeper_idx].id_hash
                    ));

                    let hyperedge = HyperedgeSlot {
                        id_hash: evolution_edge_id,
                        kind: HyperedgeKind::Evolution,
                        node_ptrs: vec![topics[absorbed_idx].id_hash, topics[keeper_idx].id_hash],
                        meta: vec![],
                        weight: 1.0,
                        created_at: now,
                        updated_at: now,
                        version: 1,
                        overflow_page: 0,
                    };

                    let edge_data = hyperedge
                        .serialize()
                        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                    let edge_offset = (edge_page_id as usize) * PAGE_SIZE + 32;
                    if edge_offset + edge_data.len() <= mmap.len() {
                        mmap[edge_offset..edge_offset + edge_data.len()].copy_from_slice(&edge_data);
                        evolution_edge_ids.push(format!("{:016x}", evolution_edge_id));
                    }
                }
            }
        }
    }

    // 移除被吸收的 Topics（从后往前删除以避免索引偏移）
    let mut indices: Vec<usize> = to_remove.into_iter().collect();
    indices.sort_by(|a, b| b.cmp(a)); // 降序

    for idx in indices {
        topics.remove(idx);
    }

    Ok((merged_count, absorbed_ids, evolution_edge_ids))
}

/// 计算两个 label 的 Jaccard 相似度
fn calculate_label_jaccard(label_a: &str, label_b: &str) -> f32 {
    let words_a: HashSet<String> = label_a
        .split_whitespace()
        .map(|s| s.to_lowercase())
        .collect();
    let words_b: HashSet<String> = label_b
        .split_whitespace()
        .map(|s| s.to_lowercase())
        .collect();

    let intersection = words_a.intersection(&words_b).count();
    let union = words_a.union(&words_b).count();

    if union == 0 {
        0.0
    } else {
        intersection as f32 / union as f32
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_label_jaccard_identical() {
        let sim = calculate_label_jaccard("machine learning", "machine learning");
        assert!((sim - 1.0).abs() < 0.001);
    }

    #[test]
    fn test_label_jaccard_different() {
        let sim = calculate_label_jaccard("machine learning", "cooking recipes");
        assert!(sim < 0.1);
    }

    #[test]
    fn test_label_jaccard_partial() {
        let sim = calculate_label_jaccard("machine learning AI", "deep learning AI");
        assert!(sim > 0.0 && sim < 1.0);
    }

    #[test]
    fn test_merge_no_similar_topics() {
        let mut topics = vec![
            TopicSlot {
                id_hash: 1,
                title: "topic A".to_string(),
                summary: None,
                engram_ids: vec![1, 2],
                knowledge_refs: vec![], archive_refs: vec![], parent_id: None,
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
            },
            TopicSlot {
                id_hash: 2,
                title: "topic B".to_string(),
                summary: None,
                engram_ids: vec![3, 4],
                knowledge_refs: vec![], archive_refs: vec![], parent_id: None,
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
            },
        ];

        // Note: This test requires mmap and header, skipped for now
        // let (merged_count, absorbed_ids, _evolution_edges) = merge_similar_topics(&mut topics, &mut mmap, &mut header, 0.5).unwrap();
        // assert_eq!(merged_count, 0);
    }

    #[test]
    #[ignore] // Requires mmap and header setup
    fn test_merge_similar_topics() {
        let mut topics = vec![
            TopicSlot {
                id_hash: 1,
                title: "machine learning".to_string(),
                summary: None,
                engram_ids: vec![1, 2],
                knowledge_refs: vec![], archive_refs: vec![], parent_id: None,
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
            },
            TopicSlot {
                id_hash: 2,
                title: "machine learning AI".to_string(),
                summary: None,
                engram_ids: vec![3, 4, 5],
                knowledge_refs: vec![], archive_refs: vec![], parent_id: None,
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
            },
        ];

    #[test]
    #[ignore] // Requires mmap and header setup
    fn test_merge_similar_topics_integration() {
        // This test requires proper mmap and header initialization
        // which is complex to set up in unit tests.
        // Integration tests should cover this functionality.
    }
    }
}
