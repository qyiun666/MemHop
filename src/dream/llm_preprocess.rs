// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

#![cfg(feature = "llm")]

//! LLM-based content preprocessing for search and write paths.
//!
//! Provides two preprocessing pipelines:
//! - **Search preprocess**: Extract semantically-equivalent keywords + judge L3 import need
//! - **Write preprocess**: Extract semantically-equivalent keywords + assign importance score
//!
//! Each pipeline uses a single LLM call with temperature=0.1 for deterministic
//! extraction. LLM failures propagate as errors — no tokenizer fallback.
//!
//! **Keyword extraction standard**: The extracted keyword list alone should allow an LLM
//! to understand the same meaning as reading the full original text. Keywords can be
//! words, phrases, sentences, or even paragraph fragments — no length or count limits.

use crate::dream::llm::LlmProvider;
use crate::query::types::{L3EntityHint, SearchPreprocessResult, WritePreprocessResult};
use crate::MemHopError;
use serde::Deserialize;

// ============================================================================
// System prompts
// ============================================================================

/// System prompt for search preprocessing — extracts semantically-equivalent
/// keywords and judges L3 knowledge graph import need.
///
/// **Semantic equivalence**: The extracted keyword list alone should allow the
/// LLM to understand the same meaning as reading the full original input.
const SYSTEM_SEARCH_PREPROCESS: &str = r#"你是 MemHop 语义压缩引擎（Search Query Optimizer）。你的职责是在检索前对用户查询进行精准优化。

## 角色定位
你是语义压缩专家，负责将自然语言查询转化为高精度检索关键词，并判断是否需要触发知识图谱导入。你必须：
- 严格遵循 JSON 输出格式
- 关键词必须可直接用于 BM25 全文检索 + 向量相似度检索
- 保留原文中所有专有名词、技术术语、版本号、数字，不得泛化或改写
- 中英文混合术语保留原文形态

## 任务A：关键词提取（语义等价标准）
核心标准：
1. **语义等价**：只看提取出的关键词列表，一个 LLM 应该能理解与阅读完整原文相同的含义
2. **无字数限制**：关键词可以是词语、短语、句子甚至段落片段
3. **无数量限制**：不限制关键词数量
4. 保留所有专有名词、技术术语、版本号、具体数值
5. 保留因果关系、条件关系、时间关系等语义结构
6. 用尽量少的关键词传达尽量完整的信息——精炼但不丢失任何语义

## 任务B：L3 知识图谱导入判断
判断标准（满足任一条即标记为需要）：
1. 查询涉及多个实体之间的关系（如"A和B的区别"、"X依赖Y"）
2. 查询涉及结构化知识推理（因果、层级、组成关系）
3. 查询涉及技能/工具的使用方法或流程
4. 查询涉及版本对比、技术选型等需要知识图谱支撑的决策

输出严格 JSON：
{"keywords":["kw1","kw2",...], "needs_l3_import":true/false, "l3_entities":[]}

如果 needs_l3_import 为 false，l3_entities 返回空数组 []。
如果 needs_l3_import 为 true，l3_entities 提取查询中涉及的实体名和类型。"#;

const SYSTEM_WRITE_PREPROCESS: &str = r#"你是 MemHop 记忆编码引擎（Memory Encoding Engine）。你的职责是对对话内容进行精准编码，提取关键词并评估重要性。

## 角色定位
你是语义压缩专家，负责将对话内容转化为可检索、可重建上下文的压缩表示。你必须：
- 严格遵循 JSON 输出格式
- 保留所有专有名词、技术术语、版本号、数字
- 中英文混合术语保留原文形态

## 任务A：关键词提取（语义等价标准）
核心标准：
1. **语义等价**：只看提取出的关键词列表，一个 LLM 应该能理解与阅读完整原文相同的含义
2. **无字数限制**：关键词可以是词语、短语、句子甚至段落片段
3. **无数量限制**：不限制关键词数量
4. 保留所有专有名词、技术术语、版本号、具体数值
5. 保留因果关系、条件关系、时间关系等语义结构
6. 用尽量少的关键词传达尽量完整的信息——精炼但不丢失任何语义

## 任务B：重要性评分（0.0-1.0）
评分标准：
- 0.0-0.2：纯闲聊、寒暄、无信息量的日常对话
- 0.3-0.5：一般信息交换、状态同步、常规讨论
- 0.6-0.8：技术决策、问题解决、知识分享
- 0.9-1.0：关键架构决策、重要Bug修复、核心知识沉淀

输出严格 JSON：
{"keywords":["kw1","kw2",...], "importance":0.0-1.0}"#;

// ============================================================================
// Internal JSON structures for deserialization
// ============================================================================

#[derive(Debug, Deserialize)]
struct SearchPreprocessJson {
    keywords: Vec<String>,
    #[serde(default)]
    needs_l3_import: bool,
    #[serde(default)]
    l3_entities: Vec<L3EntityHint>,
}

#[derive(Debug, Deserialize)]
struct WritePreprocessJson {
    keywords: Vec<String>,
    #[serde(default = "default_importance")]
    importance: f32,
}

fn default_importance() -> f32 {
    0.5
}

// ============================================================================
// Preprocessing temperature parameters
// ============================================================================

fn preprocess_top_p() -> f32 {
    0.85
}

// ============================================================================
// Generic LLM call helper — uses LlmProvider::chat()
// ============================================================================

/// Call LLM for a preprocessing task with one retry on parse failure.
/// Returns error if both initial call and retry fail.
fn call_llm_for_preprocess<T>(
    provider: &dyn LlmProvider,
    system: &str,
    user_prompt: &str,
    parse: &dyn Fn(&str) -> Result<T, MemHopError>,
    max_tokens: u32,
    temperature: f32,
) -> Result<T, MemHopError> {
    let result = provider.chat(
        system,
        user_prompt,
        max_tokens,
        temperature,
        preprocess_top_p(),
        0.0, // presence_penalty
        0.0, // frequency_penalty
    )?;

    if let Ok(val) = parse(&result) {
        return Ok(val);
    }

    // Retry once with hint
    let retry_prompt = format!(
        "{}\n\n你的上一次响应无法解析。请严格遵循 JSON 格式输出。",
        user_prompt
    );
    let response2 = provider.chat(
        system,
        &retry_prompt,
        max_tokens,
        temperature,
        preprocess_top_p(),
        0.0,
        0.0,
    )?;
    parse(&response2)
}

// ============================================================================
// Public API — Search preprocess
// ============================================================================

/// Preprocess a search query with LLM for optimized keyword extraction
/// and L3 import judgment.
pub fn preprocess_search_query(
    provider: &dyn LlmProvider,
    query: &str,
    temperature: f32,
    max_tokens: u32,
) -> Result<SearchPreprocessResult, MemHopError> {
    preprocess_search_with_llm(provider, query, temperature, max_tokens)
}

/// Build the user prompt for search preprocessing.
pub fn build_search_preprocess_prompt(query: &str) -> String {
    format!(
        "# 检索预处理\n\n原始用户查询:\n{}\n\n请完成关键词提取和 L3 导入判断，输出 JSON。",
        query
    )
}

/// Parse LLM response for search preprocessing.
///
/// Accepts any number of keywords (no count limits). Empty keyword lists are
/// accepted — validation is left to the caller.
pub fn parse_search_preprocess_response(
    response: &str,
) -> Result<SearchPreprocessResult, MemHopError> {
    let cleaned = strip_code_blocks(response);
    let json: SearchPreprocessJson = serde_json::from_str(&cleaned)
        .map_err(|e| MemHopError::Serialization(format!("Parse search preprocess JSON: {}", e)))?;

    let keywords: Vec<String> = json
        .keywords
        .into_iter()
        .filter(|k| !k.trim().is_empty())
        .collect();

    Ok(SearchPreprocessResult {
        keywords,
        needs_l3_import: json.needs_l3_import,
        l3_entities: if json.needs_l3_import {
            json.l3_entities
        } else {
            Vec::new()
        },
    })
}

// ============================================================================
// Public API — Write preprocess
// ============================================================================

/// Preprocess write content with LLM for keyword extraction and importance scoring.
pub fn preprocess_write_content(
    provider: &dyn LlmProvider,
    text: &str,
    temperature: f32,
    max_tokens: u32,
) -> Result<WritePreprocessResult, MemHopError> {
    preprocess_write_with_llm(provider, text, temperature, max_tokens)
}

/// Build the user prompt for write preprocessing.
pub fn build_write_preprocess_prompt(text: &str) -> String {
    // Use full text input; truncate only if extremely long to fit LLM context.
    let truncated = if text.len() > 4000 {
        text.chars().take(4000).collect::<String>()
    } else {
        text.to_string()
    };
    format!(
        "# 写入预处理\n\n对话内容:\n{}\n\n请完成关键词提取和重要性评分，输出 JSON。",
        truncated
    )
}

/// Parse LLM response for write preprocessing.
///
/// Accepts any number of keywords (no count limits). Empty keyword lists are
/// accepted — validation is left to the caller.
pub fn parse_write_preprocess_response(
    response: &str,
) -> Result<WritePreprocessResult, MemHopError> {
    let cleaned = strip_code_blocks(response);
    let json: WritePreprocessJson = serde_json::from_str(&cleaned)
        .map_err(|e| MemHopError::Serialization(format!("Parse write preprocess JSON: {}", e)))?;

    let keywords: Vec<String> = json
        .keywords
        .into_iter()
        .filter(|k| !k.trim().is_empty())
        .collect();

    Ok(WritePreprocessResult {
        keywords,
        importance: json.importance.clamp(0.0, 1.0),
    })
}

// ============================================================================
// LLM-backed implementations (call actual LLM)
// ============================================================================

fn preprocess_search_with_llm(
    provider: &dyn LlmProvider,
    query: &str,
    temperature: f32,
    max_tokens: u32,
) -> Result<SearchPreprocessResult, MemHopError> {
    let user_prompt = build_search_preprocess_prompt(query);
    call_llm_for_preprocess(
        provider,
        SYSTEM_SEARCH_PREPROCESS,
        &user_prompt,
        &|r| parse_search_preprocess_response(r),
        max_tokens,
        temperature,
    )
}

fn preprocess_write_with_llm(
    provider: &dyn LlmProvider,
    text: &str,
    temperature: f32,
    max_tokens: u32,
) -> Result<WritePreprocessResult, MemHopError> {
    let user_prompt = build_write_preprocess_prompt(text);
    call_llm_for_preprocess(
        provider,
        SYSTEM_WRITE_PREPROCESS,
        &user_prompt,
        &|r| parse_write_preprocess_response(r),
        max_tokens,
        temperature,
    )
}

// ============================================================================
// Utility
// ============================================================================

/// Strip markdown code block markers from LLM response.
fn strip_code_blocks(s: &str) -> String {
    let trimmed = s.trim();
    if let Some(stripped) = trimmed.strip_prefix("```") {
        let start = stripped.find('\n').map(|i| i + 1).unwrap_or(0);
        let rest = &stripped[start..];
        let end = rest.rfind("```").unwrap_or(rest.len());
        rest[..end].trim().to_string()
    } else {
        trimmed.to_string()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_search_response_valid() {
        let response = r#"{"keywords":["Rust","memhop","BM25检索"],"needs_l3_import":true,"l3_entities":[{"name":"BM25","type":"concept"}]}"#;
        let result = parse_search_preprocess_response(response).unwrap();
        assert_eq!(result.keywords.len(), 3);
        assert!(result.needs_l3_import);
        assert_eq!(result.l3_entities.len(), 1);
    }

    #[test]
    fn test_parse_search_response_no_l3() {
        let response =
            r#"{"keywords":["hello","world","test"],"needs_l3_import":false,"l3_entities":[]}"#;
        let result = parse_search_preprocess_response(response).unwrap();
        assert!(!result.needs_l3_import);
        assert!(result.l3_entities.is_empty());
    }

    #[test]
    fn test_parse_write_response_valid() {
        let response = r#"{"keywords":["cargo build","release mode","编译错误"],"importance":0.8}"#;
        let result = parse_write_preprocess_response(response).unwrap();
        assert_eq!(result.keywords.len(), 3);
        assert!((result.importance - 0.8).abs() < 0.01);
    }

    #[test]
    fn test_strip_code_blocks() {
        let input = "```json\n{\"key\":\"value\"}\n```";
        let result = strip_code_blocks(input);
        assert_eq!(result, "{\"key\":\"value\"}");
    }
}
