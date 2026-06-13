use crate::file::page::decode_page_ref;
use crate::index::sparse::SparseIndex;
use crate::slot::engram::EngramSlot;
use crate::slot::topic::TopicSlot;
use crate::util::PAGE_SIZE;
use crate::MemHopError;
use std::collections::{HashMap, HashSet};

pub mod cooccurrence;
pub mod merge;
pub mod reflect;

/// 中英文停用词列表（80+ 常见停用词）
const STOP_WORDS: &[&str] = &[
    // 英文停用词
    "the", "a", "an", "is", "are", "was", "were", "be", "been", "being", "have", "has", "had", "do",
    "does", "did", "will", "would", "could", "should", "may", "might", "can", "shall", "to", "of",
    "in", "for", "on", "with", "at", "by", "from", "as", "into", "through", "during", "before",
    "after", "above", "below", "between", "out", "off", "over", "under", "again", "further",
    "then", "once", "here", "there", "when", "where", "why", "how", "all", "both", "each", "few",
    "more", "most", "other", "some", "such", "no", "nor", "not", "only", "own", "same", "so",
    "than", "too", "very", "just", "and", "but", "if", "or", "because", "until", "while", "this",
    "that", "these", "those", "i", "me", "my", "we", "our", "you", "your", "he", "him", "his",
    "she", "her", "it", "its", "they", "them", "their", "what", "which", "who",
    // 中文停用词
    "的", "了", "在", "是", "我", "有", "和", "就", "不", "人", "都", "一", "一个", "上", "也",
    "很", "到", "说", "要", "去", "你", "会", "着", "没有", "看", "好", "自己", "这", "他", "她",
    "它", "们", "那", "些", "什么", "怎么", "吗", "呢", "吧", "啊", "哦", "嗯", "呀",
];

/// 从文本中提取关键词
///
/// # 参数
/// * `text` - 输入文本
/// * `max_keywords` - 最大关键词数量
///
/// # 返回
/// 按重要性排序的关键词列表
pub fn extract_keywords(text: &str, max_keywords: usize) -> Vec<String> {
    // 1. 分词（简单按空格和标点分割）
    let words = tokenize(text);

    // 2. 过滤停用词和短词
    let filtered: Vec<String> = words
        .iter()
        .filter(|word| {
            let lower = word.to_lowercase();
            !STOP_WORDS.contains(&lower.as_str()) && word.len() > 2
        })
        .cloned()
        .collect();

    // 3. 统计词频
    let mut freq_map: HashMap<String, u32> = HashMap::new();
    for word in &filtered {
        let lower = word.to_lowercase();
        *freq_map.entry(lower).or_insert(0) += 1;
    }

    // 4. 按频率和长度排序
    let mut keywords: Vec<(String, u32)> = freq_map.into_iter().collect();
    keywords.sort_by(|a, b| {
        // 优先按频率，其次按长度
        b.1.cmp(&a.1).then_with(|| b.0.len().cmp(&a.0.len()))
    });

    // 5. 返回 top-k
    keywords
        .into_iter()
        .take(max_keywords)
        .map(|(word, _)| word)
        .collect()
}

/// 简单分词器
fn tokenize(text: &str) -> Vec<String> {
    text.split(|c: char| c.is_whitespace() || c.is_ascii_punctuation())
        .filter(|s| !s.is_empty())
        .map(|s| s.to_string())
        .collect()
}

/// 检测两个节点之间是否存在话题边界
///
/// # 参数
/// * `node_a_id` - 第一个节点的 ID
/// * `node_b_id` - 第二个节点的 ID
/// * `data` - mmap 数据切片
/// * `btree` - B树索引
/// * `vector_dim` - 向量维度
///
/// # 返回
/// true 表示存在边界（话题切换），false 表示连续
pub fn detect_topic_boundary(
    node_a_id: &str,
    node_b_id: &str,
    data: &[u8],
    btree: &crate::index::btree::BTreeIndex,
    vector_dim: usize,
) -> Result<bool, crate::MemHopError> {
    // 尝试使用向量相似度
    if let (Some(vec_a), Some(vec_b)) = (
        load_vector(node_a_id, data, btree, vector_dim)?,
        load_vector(node_b_id, data, btree, vector_dim)?,
    ) {
        let similarity = cosine_similarity(&vec_a, &vec_b);
        Ok(similarity < 0.3) // 余弦相似度 < 0.3 判定为边界
    } else {
        // Fallback 到文本 Jaccard 相似度
        let text_a = load_text(node_a_id, data, btree)?;
        let text_b = load_text(node_b_id, data, btree)?;

        let jaccard = calculate_jaccard_similarity(&text_a, &text_b);
        Ok(jaccard < 0.1) // Jaccard < 0.1 判定为边界
    }
}

/// 加载节点的向量
fn load_vector(
    node_id: &str,
    data: &[u8],
    btree: &crate::index::btree::BTreeIndex,
    vector_dim: usize,
) -> Result<Option<Vec<f32>>, crate::MemHopError> {
    use crate::file::page::decode_page_ref;
    use crate::slot::engram::EngramSlot;
    use crate::util::PAGE_SIZE;

    let id_hash = crate::util::hash::hash_id(node_id);

    if let Some(page_ref) = btree.search(id_hash) {
        let (page_id, _slot_index) = decode_page_ref(page_ref);
        let offset = (page_id as usize) * PAGE_SIZE + 32;

        if offset + 128 <= data.len() {
            let engram = EngramSlot::deserialize(&data[offset..])?;

            // 从 vector_page_ref 读取向量
            if engram.vector_page_ref != 0 {
                let (vec_page_id, _vec_slot_index) = decode_page_ref(engram.vector_page_ref);
                let vec_offset = (vec_page_id as usize) * PAGE_SIZE + 32;

                if vec_offset + 16 + vector_dim * 2 <= data.len() {
                    let vector_data = &data[vec_offset + 16..vec_offset + 16 + vector_dim * 2];
                    let vector: Vec<f32> = vector_data
                        .chunks(2)
                        .map(|chunk| {
                            if chunk.len() == 2 {
                                let bytes = [chunk[0], chunk[1]];
                                crate::util::f16::from_le_bytes(bytes)
                            } else {
                                0.0
                            }
                        })
                        .collect();
                    return Ok(Some(vector));
                }
            }
        }
    }

    Ok(None)
}

/// 加载节点的文本
fn load_text(
    node_id: &str,
    data: &[u8],
    btree: &crate::index::btree::BTreeIndex,
) -> Result<String, crate::MemHopError> {
    use crate::file::page::decode_page_ref;
    use crate::slot::engram::EngramSlot;
    use crate::util::PAGE_SIZE;

    let id_hash = crate::util::hash::hash_id(node_id);

    if let Some(page_ref) = btree.search(id_hash) {
        let (page_id, _slot_index) = decode_page_ref(page_ref);
        let offset = (page_id as usize) * PAGE_SIZE + 32;

        if offset + 128 <= data.len() {
            let engram = EngramSlot::deserialize(&data[offset..])?;
            return Ok(engram.text);
        }
    }

    Ok(String::new())
}

/// 计算两个文本的 Jaccard 相似度（基于 ngram）
fn calculate_jaccard_similarity(text_a: &str, text_b: &str) -> f32 {
    let ngrams_a: HashSet<String> = generate_ngrams(text_a, 3);
    let ngrams_b: HashSet<String> = generate_ngrams(text_b, 3);

    let intersection = ngrams_a.intersection(&ngrams_b).count();
    let union = ngrams_a.union(&ngrams_b).count();

    if union == 0 {
        0.0
    } else {
        intersection as f32 / union as f32
    }
}

/// 生成 n-gram 集合
fn generate_ngrams(text: &str, n: usize) -> HashSet<String> {
    let chars: Vec<char> = text.chars().collect();
    let mut ngrams = HashSet::new();

    if chars.len() >= n {
        for i in 0..=chars.len() - n {
            let ngram: String = chars[i..i + n].iter().collect();
            ngrams.insert(ngram);
        }
    }

    ngrams
}

/// 计算两个向量的余弦相似度
fn cosine_similarity(vec_a: &[f32], vec_b: &[f32]) -> f32 {
    if vec_a.len() != vec_b.len() || vec_a.is_empty() {
        return 0.0;
    }

    let dot_product: f32 = vec_a.iter().zip(vec_b.iter()).map(|(a, b)| a * b).sum();
    let norm_a: f32 = vec_a.iter().map(|x| x * x).sum::<f32>().sqrt();
    let norm_b: f32 = vec_b.iter().map(|x| x * x).sum::<f32>().sqrt();

    if norm_a < f32::EPSILON || norm_b < f32::EPSILON {
        0.0
    } else {
        dot_product / (norm_a * norm_b)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_keywords_basic() {
        let text = "machine learning is a subset of artificial intelligence";
        let keywords = extract_keywords(text, 5);

        assert!(!keywords.is_empty());
        assert!(keywords.contains(&"machine".to_string()));
        assert!(keywords.contains(&"learning".to_string()));
        assert!(keywords.contains(&"artificial".to_string()));
        assert!(keywords.contains(&"intelligence".to_string()));
    }

    #[test]
    fn test_extract_keywords_filters_stopwords() {
        let text = "the cat is on the mat";
        let keywords = extract_keywords(text, 5);

        // "the", "is", "on" 应该被过滤
        assert!(!keywords.contains(&"the".to_string()));
        assert!(!keywords.contains(&"is".to_string()));
        assert!(!keywords.contains(&"on".to_string()));
    }

    #[test]
    fn test_extract_keywords_chinese() {
        let text = "机器学习 是 人工智能 的 一个 分支";
        let keywords = extract_keywords(text, 5);

        // 中文停用词应该被过滤
        assert!(!keywords.contains(&"的".to_string()));
        assert!(!keywords.contains(&"是".to_string()));
    }

    #[test]
    fn test_extract_keywords_limit() {
        let text = "one two three four five six seven eight nine ten";
        let keywords = extract_keywords(text, 3);

        assert_eq!(keywords.len(), 3);
    }

    #[test]
    fn test_jaccard_similarity_identical() {
        let sim = calculate_jaccard_similarity("hello world", "hello world");
        assert!((sim - 1.0).abs() < 0.001);
    }

    #[test]
    fn test_jaccard_similarity_different() {
        let sim = calculate_jaccard_similarity("hello world", "foo bar");
        assert!(sim < 0.1);
    }

    #[test]
    fn test_jaccard_similarity_partial() {
        let sim = calculate_jaccard_similarity("hello world foo", "hello world bar");
        assert!(sim > 0.0 && sim < 1.0);
    }

    #[test]
    fn test_tokenize_basic() {
        let tokens = tokenize("hello world test");
        assert_eq!(tokens.len(), 3);
        assert_eq!(tokens[0], "hello");
    }

    #[test]
    fn test_tokenize_with_punctuation() {
        let tokens = tokenize("hello, world! test.");
        assert_eq!(tokens.len(), 3);
    }
}

// Re-export public APIs
pub use cooccurrence::create_cooccurrence_hyperedges;
pub use merge::merge_similar_topics;
pub use reflect::reflect_topic;

/// Organize 操作报告
#[derive(Debug, Clone)]
pub struct OrganizeReport {
    pub keywords_updated: u32,             // 更新的关键词数量
    pub topics_merged: u32,                // 合并的 Topic 数量
    pub topics_reflected: u32,             // 反思的 Topic 数量
    pub cooccurrence_edges: Vec<String>,   // 创建的共现超边 ID 列表
}

/// 执行完整的 Organize 流程
///
/// # 参数
/// * `topics` - 所有 Topic 列表（可变引用，会被修改）
/// * `mmap` - Mutable memory-mapped file
/// * `header` - File header for free list management
/// * `btree` - B树索引
/// * `sparse_index` - 稀疏索引
/// * `session_topics` - 当前会话激活的 Topic IDs
/// * `merge_threshold` - Topic 合并阈值（默认 0.5）
///
/// # 返回
/// OrganizeReport 报告
pub fn organize(
    topics: &mut Vec<TopicSlot>,
    mmap: &mut memmap2::MmapMut,
    header: &mut crate::file::header::FileHeader,
    btree: &crate::index::btree::BTreeIndex,
    sparse_index: &SparseIndex,
    session_topics: &HashSet<u64>,
    merge_threshold: f32,
) -> Result<OrganizeReport, crate::MemHopError> {
    let mut report = OrganizeReport {
        keywords_updated: 0,
        topics_merged: 0,
        topics_reflected: 0,
        cooccurrence_edges: Vec::new(),
    };

    // Step 1: 对每个 Topic 进行反思（生成摘要）并写回关键词
    for topic in topics.iter() {
        let id_hex = format!("{:016x}", topic.id_hash);
        if let Ok(Some(summary)) = reflect::reflect_topic(&id_hex, mmap, btree, sparse_index) {
            report.topics_reflected += 1;

            // 提取关键词并写回对应的 Engrams
            let keywords = extract_keywords(&summary, 10);

            // 遍历 topic 关联的 node_ids (即 Engram IDs)
            for engram_id in &topic.node_ids {
                if let Some(page_ref) = btree.search(*engram_id) {
                    let (page_id, _) = decode_page_ref(page_ref);
                    let offset = (page_id as usize) * PAGE_SIZE + 32;

                    if offset < mmap.len() {
                        if let Ok(mut engram) = EngramSlot::deserialize(&mmap[offset..]) {
                            engram.keywords = keywords.clone();
                            let data = engram
                                .serialize()
                                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

                            if offset + data.len() <= mmap.len() {
                                mmap[offset..offset + data.len()].copy_from_slice(&data);
                                report.keywords_updated += 1;
                            }
                        }
                    }
                }
            }
        }
    }

    // Step 2: 合并相似 Topics
    let (merged_count, _absorbed_ids, _evolution_edges) = merge::merge_similar_topics(topics, mmap, header, merge_threshold)?;
    report.topics_merged = merged_count;

    // Step 3: 创建共现超边
    report.cooccurrence_edges =
        cooccurrence::create_cooccurrence_hyperedges(mmap, header, topics, session_topics)?;

    // Step 4: 更新关键词（已完成）
    // keywords_updated 已在 Step 1 中累加

    Ok(report)
}
