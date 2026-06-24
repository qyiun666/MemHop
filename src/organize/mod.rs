//! Organize module — keyword extraction utilities
//!
//! This module provides text analysis utilities used across the MemHop codebase,
//! primarily for keyword extraction from text content.

use crate::index::sparse::tokenize;
use std::collections::HashMap;

/// 从文本中提取关键词
///
/// # 参数
/// * `text` - 输入文本
/// * `max_keywords` - 最大关键词数量
///
/// # 返回
/// 按重要性排序的关键词列表
pub fn extract_keywords(text: &str, max_keywords: usize) -> Vec<String> {
    // 1. 分词（CJK 使用 jieba，英文使用空格，已过滤停用词）
    let words = tokenize(text);

    // 2. 过滤短词
    let filtered: Vec<String> = words.into_iter().filter(|word| word.len() > 2).collect();

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
