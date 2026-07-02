// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Keyword extraction utilities for text content analysis.

use crate::index::sparse::tokenize;
use std::collections::HashMap;

pub fn extract_keywords(text: &str, max_keywords: usize) -> Vec<String> {
    let words = tokenize(text);

    let filtered: Vec<String> = words.into_iter().filter(|word| word.len() > 2).collect();

    let mut freq_map: HashMap<String, u32> = HashMap::new();
    for word in &filtered {
        let lower = word.to_lowercase();
        *freq_map.entry(lower).or_insert(0) += 1;
    }

    let mut keywords: Vec<(String, u32)> = freq_map.into_iter().collect();
    keywords.sort_by(|a, b| {
        b.1.cmp(&a.1).then_with(|| b.0.len().cmp(&a.0.len()))
    });

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

        assert!(!keywords.contains(&"the".to_string()));
        assert!(!keywords.contains(&"is".to_string()));
        assert!(!keywords.contains(&"on".to_string()));
    }

    #[test]
    fn test_extract_keywords_chinese() {
        let text = "机器学习 是 人工智能 的 一个 分支";
        let keywords = extract_keywords(text, 5);

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
