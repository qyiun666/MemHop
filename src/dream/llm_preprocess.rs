// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

#![cfg(feature = "llm")]

//! LLM-based content preprocessing for search and write paths.
//!
//! Provides two preprocessing pipelines:
//! - **Search preprocess**: Extract precise keywords + judge L3 import need
//! - **Write preprocess**: Extract precise keywords + assign importance score
//!
//! Each pipeline has a well-designed system prompt, temperature=0.1 for
//! deterministic extraction, and a non-LLM fallback via the tokenizer.

use crate::dream::llm::LlmProvider;
use crate::query::types::{L3EntityHint, SearchPreprocessResult, WritePreprocessResult};
use crate::MemHopError;
use serde::Deserialize;

// ============================================================================
// System prompts
// ============================================================================

/// System prompt for search preprocessing — extracts precise keywords and
/// judges L3 knowledge graph import need.
const SYSTEM_SEARCH_PREPROCESS: &str = r#"你是 MemHop 检索优化引擎（Search Query Optimizer）。你的职责是在检索前对用户查询进行精准优化。

## 角色定位
你是信息检索预处理专家，负责将自然语言查询转化为高精度检索关键词，并判断是否需要触发知识图谱导入。你必须：
- 严格遵循 JSON 输出格式
- 关键词必须可直接用于 BM25 全文检索 + 向量相似度检索
- 保留原文中所有专有名词、技术术语、版本号、数字，不得泛化或改写
- 中英文混合术语保留原文形态

## 任务A：精准关键词提取（5-10个）
提取规则：
1. **名词优先**：优先提取名词性短语（2-4字组合），而非单个字词。例如"用户认证流程"而非"用户"+"认证"
2. **技术术语保留**：API名称、框架名、协议名、算法名保持原样，如"JWT token"、"React hooks"、"BM25"
3. **实体名保留**：人名、产品名、公司名保持原样
4. **动作+对象组合**：对于操作性查询，提取"动作+对象"短语，如"部署配置"、"性能优化"
5. **去重排序**：按检索重要性降序排列，最核心的关键词排在前面
6. **覆盖全面**：确保关键词覆盖查询的所有主要意图维度

## 任务B：L3 知识图谱导入判断
判断标准（满足任一条即标记为需要）：
1. 查询涉及多个实体之间的关系（如"A和B的区别"、"X依赖Y"）
2. 查询涉及结构化知识推理（因果、层级、组成关系）
3. 查询涉及技能/工具的使用方法或流程
4. 查询涉及版本对比、技术选型等需要知识图谱支撑的决策

输出 JSON：
{"keywords":["kw1","kw2",...], "needs_l3_import":true/false, "l3_entities":[]}

如果 needs_l3_import 为 false，l3_entities 返回空数组 []。
如果 needs_l3_import 为 true，l3_entities 提取查询中涉及的实体名和类型。"#;

const SYSTEM_WRITE_PREPROCESS: &str = r#"你是 MemHop 记忆编码引擎（Memory Encoding Engine）。你的职责是对对话内容进行精准编码，提取关键词并评估重要性。

## 角色定位
你是记忆编码专家，负责将对话内容转化为可检索、可重建上下文的压缩表示。你必须：
- 严格遵循 JSON 输出格式
- 保留所有专有名词、技术术语、版本号、数字
- 中英文混合术语保留原文形态

## 任务A：精准关键词提取（5-10个）
提取规则：
1. **关键动作**：提取对话中的关键操作、决策、结论，如"同意使用Redis"、"决定重构数据库层"
2. **技术实体**：代码库名、API、框架、工具名，如"memhop::search_context"、"cargo build"
3. **问题与解决**：如果有问题-解决模式，提取问题和方案关键词
4. **领域概念**：对话涉及的专业概念、术语
5. **去重排序**：按对检索和上下文重建的重要性降序排列

## 任务B：重要性评分（0.0-1.0）
评分标准：
- 0.0-0.2：纯闲聊、寒暄、无信息量的日常对话
- 0.3-0.5：一般信息交换、状态同步、常规讨论
- 0.6-0.8：技术决策、问题解决、知识分享
- 0.9-1.0：关键架构决策、重要Bug修复、核心知识沉淀

输出 JSON：
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

/// Low temperature for extraction tasks — deterministic, precise.
fn preprocess_temperature() -> f32 {
    0.1
}
fn preprocess_top_p() -> f32 {
    0.85
}
fn preprocess_max_tokens() -> u32 {
    512
}

// ============================================================================
// Generic LLM call helper — uses LlmProvider::chat()
// ============================================================================

/// Call LLM for a preprocessing task with one retry on parse failure.
/// `fallback_fn` is lazily evaluated only when the LLM call or parse fails.
fn call_llm_for_preprocess<T, F>(
    provider: &dyn LlmProvider,
    system: &str,
    user_prompt: &str,
    parse: &dyn Fn(&str) -> Result<T, MemHopError>,
    fallback_fn: F,
) -> T
where
    F: FnOnce() -> T,
{
    let result = provider.chat(
        system,
        user_prompt,
        preprocess_max_tokens(),
        preprocess_temperature(),
        preprocess_top_p(),
        0.0, // presence_penalty
        0.0, // frequency_penalty
    );

    match result {
        Ok(response) => {
            if let Ok(val) = parse(&response) {
                return val;
            }
            // Retry once with hint
            let retry_prompt = format!(
                "{}\n\n你的上一次响应无法解析。请严格遵循 JSON 格式输出。",
                user_prompt
            );
            if let Ok(response2) = provider.chat(
                system,
                &retry_prompt,
                preprocess_max_tokens(),
                preprocess_temperature(),
                preprocess_top_p(),
                0.0,
                0.0,
            ) {
                if let Ok(val) = parse(&response2) {
                    return val;
                }
            }
            tracing::warn!("LLM preprocess parse failed after retry, using fallback");
            fallback_fn()
        }
        Err(e) => {
            tracing::warn!("LLM preprocess chat failed: {}, using fallback", e);
            fallback_fn()
        }
    }
}

// ============================================================================
// Public API — Search preprocess
// ============================================================================

/// Preprocess a search query with LLM for optimized keyword extraction
/// and L3 import judgment. Falls back to tokenizer-based extraction
/// when LLM is unavailable.
pub fn preprocess_search_query(
    provider: Option<&dyn LlmProvider>,
    query: &str,
) -> SearchPreprocessResult {
    if let Some(p) = provider {
        if let Ok(result) = preprocess_search_with_llm(p, query) {
            return result;
        }
        tracing::warn!("LLM search preprocess failed, falling back to tokenizer");
    }
    fallback_search_preprocess(query)
}

/// Build the user prompt for search preprocessing.
pub fn build_search_preprocess_prompt(query: &str) -> String {
    format!(
        "# 检索预处理\n\n原始用户查询:\n{}\n\n请完成关键词提取和 L3 导入判断，输出 JSON。",
        query
    )
}

/// Parse LLM response for search preprocessing.
pub fn parse_search_preprocess_response(response: &str) -> Result<SearchPreprocessResult, MemHopError> {
    let cleaned = strip_code_blocks(response);
    let json: SearchPreprocessJson = serde_json::from_str(&cleaned)
        .map_err(|e| MemHopError::Serialization(format!("Parse search preprocess JSON: {}", e)))?;

    // Enforce keyword count limits
    let keywords: Vec<String> = json
        .keywords
        .into_iter()
        .filter(|k| !k.trim().is_empty())
        .take(10)
        .collect();

    if keywords.is_empty() {
        return Err(MemHopError::Serialization(
            "Search preprocess: no keywords extracted".into(),
        ));
    }

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
/// Falls back to tokenizer-based extraction when LLM is unavailable.
pub fn preprocess_write_content(
    provider: Option<&dyn LlmProvider>,
    text: &str,
) -> WritePreprocessResult {
    if let Some(p) = provider {
        if let Ok(result) = preprocess_write_with_llm(p, text) {
            return result;
        }
        tracing::warn!("LLM write preprocess failed, falling back to tokenizer");
    }
    fallback_write_preprocess(text)
}

/// Build the user prompt for write preprocessing.
pub fn build_write_preprocess_prompt(text: &str) -> String {
    format!(
        "# 写入预处理\n\n对话内容:\n{}\n\n请完成关键词提取和重要性评分，输出 JSON。",
        text.chars().take(1000).collect::<String>()
    )
}

/// Parse LLM response for write preprocessing.
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
        .take(10)
        .collect();

    if keywords.is_empty() {
        return Err(MemHopError::Serialization(
            "Write preprocess: no keywords extracted".into(),
        ));
    }

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
) -> Result<SearchPreprocessResult, MemHopError> {
    let user_prompt = build_search_preprocess_prompt(query);
    let result = call_llm_for_preprocess(
        provider,
        SYSTEM_SEARCH_PREPROCESS,
        &user_prompt,
        &|r| parse_search_preprocess_response(r),
        || fallback_search_preprocess(query),
    );
    // call_llm_for_preprocess always returns a value (via fallback),
    // but we wrap it in Ok to match the expected return type.
    Ok(result)
}

fn preprocess_write_with_llm(
    provider: &dyn LlmProvider,
    text: &str,
) -> Result<WritePreprocessResult, MemHopError> {
    let user_prompt = build_write_preprocess_prompt(text);
    let result = call_llm_for_preprocess(
        provider,
        SYSTEM_WRITE_PREPROCESS,
        &user_prompt,
        &|r| parse_write_preprocess_response(r),
        || fallback_write_preprocess(text),
    );
    Ok(result)
}

// ============================================================================
// Fallback — non-LLM tokenizer-based extraction
// ============================================================================

/// Fallback: tokenizer-based search keyword extraction.
fn fallback_search_preprocess(query: &str) -> SearchPreprocessResult {
    let keywords = crate::organize::extract_keywords(query, 8);
    SearchPreprocessResult {
        keywords,
        needs_l3_import: false,
        l3_entities: Vec::new(),
    }
}

/// Fallback: tokenizer-based write keyword extraction with default importance.
fn fallback_write_preprocess(text: &str) -> WritePreprocessResult {
    let keywords = crate::organize::extract_keywords(text, 8);
    let importance = if text.len() < 20 {
        0.2 // short messages likely casual
    } else if text.len() > 200 {
        0.7 // longer messages likely substantive
    } else {
        0.5
    };
    WritePreprocessResult {
        keywords,
        importance,
    }
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
        let response = r#"{"keywords":["hello","world","test"],"needs_l3_import":false,"l3_entities":[]}"#;
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
    fn test_fallback_search_produces_keywords() {
        let result = fallback_search_preprocess("machine learning is important for AI");
        assert!(!result.keywords.is_empty());
        assert!(!result.needs_l3_import);
    }

    #[test]
    fn test_fallback_write_produces_keywords() {
        let result = fallback_write_preprocess("short msg");
        assert!(!result.keywords.is_empty());
        assert!(result.importance < 0.5); // short message
    }

    #[test]
    fn test_strip_code_blocks() {
        let input = "```json\n{\"key\":\"value\"}\n```";
        let result = strip_code_blocks(input);
        assert_eq!(result, "{\"key\":\"value\"}");
    }
}
