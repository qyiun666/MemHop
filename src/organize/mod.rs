//! Organize module — keyword extraction utilities
//!
//! This module provides text analysis utilities used across the MemHop codebase,
//! primarily for keyword extraction from text content.

use std::collections::HashMap;

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
