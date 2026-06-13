use crate::file::free_list::allocate_from_free_list;
use crate::file::header::FileHeader;
use crate::slot::hyperedge::{HyperedgeKind, HyperedgeSlot};
use crate::slot::topic::TopicSlot;
use crate::util::{get_current_timestamp, hash_id, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::{HashMap, HashSet};

/// 创建 Topic 共现超边
///
/// # 参数
/// * `mmap` - Mutable memory-mapped file
/// * `header` - File header for free list management
/// * `topics` - Topic 列表
/// * `session_topics` - 当前会话中激活的 Topic IDs
///
/// # 返回
/// 创建的共现超边 ID 列表（hex 格式）
pub fn create_cooccurrence_hyperedges(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    _topics: &[TopicSlot],
    session_topics: &HashSet<u64>,
) -> Result<Vec<String>, MemHopError> {
    // 统计 session 内 Topic 共现次数
    let mut cooccurrence_count: HashMap<(u64, u64), u32> = HashMap::new();

    // 简化实现：假设同一 session 中的 topics 都共现
    // 实际应用中需要从会话历史中统计
    let session_topic_vec: Vec<u64> = session_topics.iter().copied().collect();

    for i in 0..session_topic_vec.len() {
        for j in (i + 1)..session_topic_vec.len() {
            let key = if session_topic_vec[i] < session_topic_vec[j] {
                (session_topic_vec[i], session_topic_vec[j])
            } else {
                (session_topic_vec[j], session_topic_vec[i])
            };

            *cooccurrence_count.entry(key).or_insert(0) += 1;
        }
    }

    // 共现 >= 2 次则创建超边
    let mut edge_ids = Vec::new();
    for ((topic_a, topic_b), count) in &cooccurrence_count {
        if *count >= 2 {
            // Allocate page for hyperedge
            if let Ok(edge_page_id) = allocate_from_free_list(mmap, header) {
                let now = get_current_timestamp();

                // 权重 = min(count, 5) / 5.0
                let weight = (*count).min(5) as f32 / 5.0;

                let edge_id_hash = hash_id(&format!("cooccur_{}_{}", topic_a, topic_b));
                let hyperedge = HyperedgeSlot {
                    id_hash: edge_id_hash,
                    kind: HyperedgeKind::CoOccurrence,
                    node_ptrs: vec![*topic_a, *topic_b],
                    meta: vec![],
                    weight,
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
                    // Track the created edge ID
                    edge_ids.push(format!("{:016x}", edge_id_hash));
                }
            }
        }
    }

    Ok(edge_ids)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    #[test]
    fn test_cooccurrence_no_edges() {
        let session_topics = HashSet::new();
        let topics = vec![];

        // Create a temporary mmap for testing
        let temp_file = tempfile::NamedTempFile::new().unwrap();
        let path = temp_file.path();

        let mut file = std::fs::File::create(path).unwrap();
        file.write_all(&vec![0u8; 4096 * 10]).unwrap();
        drop(file);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = FileHeader::new(768);

        let edges =
            create_cooccurrence_hyperedges(&mut mmap, &mut header, &topics, &session_topics)
                .unwrap();
        assert_eq!(edges.len(), 0);
    }

    #[test]
    fn test_cooccurrence_single_topic() {
        let mut session_topics = HashSet::new();
        session_topics.insert(1);
        let topics = vec![];

        let temp_file = tempfile::NamedTempFile::new().unwrap();
        let path = temp_file.path();

        let mut file = std::fs::File::create(path).unwrap();
        file.write_all(&vec![0u8; 4096 * 10]).unwrap();
        drop(file);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = FileHeader::new(768);

        let edges =
            create_cooccurrence_hyperedges(&mut mmap, &mut header, &topics, &session_topics)
                .unwrap();
        assert_eq!(edges.len(), 0); // 单个 topic 无法形成共现
    }

    #[test]
    fn test_cooccurrence_basic() {
        let mut session_topics = HashSet::new();
        session_topics.insert(1);
        session_topics.insert(2);
        session_topics.insert(3);

        let topics = vec![];

        let temp_file = tempfile::NamedTempFile::new().unwrap();
        let path = temp_file.path();

        let mut file = std::fs::File::create(path).unwrap();
        file.write_all(&vec![0u8; 4096 * 10]).unwrap();
        drop(file);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = FileHeader::new(768);

        // 3 个 topics 会产生 C(3,2) = 3 对，但 count=1 < 2，所以不会创建边
        let edges =
            create_cooccurrence_hyperedges(&mut mmap, &mut header, &topics, &session_topics)
                .unwrap();
        assert_eq!(edges.len(), 0);
    }
}
