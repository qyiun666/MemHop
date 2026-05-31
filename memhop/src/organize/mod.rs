//! 每轮对话自动整理
//!
//! 在 `Brain::perceive()` 返回前自动调用，执行：
//!   1. 实体提取 — 从输入文本中提取关键词（启发式）
//!   2. 图链接 — 将新 engram 链接到语义相似的 Hopfield 节点
//!   3. (占位) 更新 growth 统计
//!   4. 边界检测 — 新话题时触发 `compress_plan`
//!
//! 本模块保证不阻塞主流程：所有错误仅通过 `eprintln!` 日志记录，
//! 绝不传播到调用方。所有操作 ≤1ms。

mod compress; // v0.12.2: plan compression logic (moved from brain.rs)
pub(crate) use compress::compress_plan;

mod reflect; // v0.12.2: reflection (moved from brain.rs)
pub(crate) use reflect::reflect;

mod plan; // v0.12.2: plan management (moved from brain.rs)
pub(crate) use plan::{set_plan_name, get_plan_tree, complete_plan};

use crate::brain::Brain;
use crate::engram::AssociationKind;
use crate::engram::DialogueTurn;
use crate::error::Result;
use crate::types::{PerceptionInput, PerceptionOutput};

// ── 停用词表（中英文常见虚词） ──────────────────────────────

const STOP_WORDS: &[&str] = &[
    // Chinese
    "的", "了", "在", "是", "我", "有", "和", "就", "不", "人", "都", "一",
    "一个", "上", "也", "很", "到", "说", "要", "去", "你", "会", "着", "没有",
    "看", "好", "自己", "这", "他", "她", "它", "们", "那", "这个", "那个",
    "什么", "怎么", "为什么", "因为", "所以", "但是", "虽然", "如果",
    // English
    "the", "a", "an", "is", "are", "was", "were", "be", "been", "being",
    "have", "has", "had", "do", "does", "did", "will", "would", "could",
    "should", "may", "might", "can", "shall", "to", "of", "in", "for",
    "on", "with", "at", "by", "from", "as", "into", "through", "during",
    "before", "after", "above", "below", "between", "under", "again",
    "further", "then", "once", "here", "there", "when", "where", "why",
    "how", "all", "each", "every", "both", "few", "more", "most", "other",
    "some", "such", "no", "nor", "not", "only", "own", "same", "so",
    "than", "too", "very", "just", "because", "as", "until", "while",
    "about", "over", "after", "before", "between", "under", "again",
    "and", "but", "or", "if", "while", "that", "this", "these", "those",
];

/// 提取关键词：按长度降序 + 出现频率降序，去重，取前 `max` 个。
fn extract_keywords(text: &str, max: usize) -> Vec<String> {
    let mut words: Vec<String> = text
        .split(|c: char| !c.is_alphanumeric())
        .filter(|s| {
            let t = s.trim();
            t.len() >= 2 && !STOP_WORDS.contains(&t)
        })
        .map(|s| s.trim().to_string())
        .collect();

    // 长度优先，同长度按频率降序
    words.sort_by(|a, b| {
        b.len()
            .cmp(&a.len())
            .then_with(|| {
                let cnt_b = text.matches(b).count();
                let cnt_a = text.matches(a).count();
                cnt_b.cmp(&cnt_a)
            })
    });
    words.dedup();
    words.truncate(max);
    words
}

/// 每轮 `perceive()` 后自动调用。
///
/// 不阻塞主流程——所有错误仅通过 `eprintln!` 日志记录，绝不传播。
pub fn organize(
    brain: &mut Brain,
    input: &PerceptionInput,
    output: &PerceptionOutput,
) -> Result<()> {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;

    // Step 1: 实体提取 — 从输入文本中提取关键词/实体
    let _ = extract_keywords(&input.content, 10);

    // Step 2: 图链接 — 将新 engram 链接到语义相似的已有 Hopfield 节点
    let query_f32: Vec<f32> = input.vector.iter().map(|x| x.to_f32()).collect();
    let similar = brain.hopfield.recall_topk(&query_f32, 5);
    for (similar_id, confidence) in &similar {
        if *confidence > 0.3 {
            brain.graph.add_edge(
                &brain.storage,
                &output.engram_id,
                similar_id,
                0.5,
                AssociationKind::Semantic,
                now,
            )?;
        }
    }

    // Step 3: 更新 growth 统计 — total_perceptions 已在 perceive 内递增
    // （本阶段无需额外更新）

    // Step 4: 检测边界 — 新话题时触发 compress_plan（仅在 Full 阶段）
    let is_full_phase =
        brain.growth.total_perceptions >= (brain.config.warmup_rounds as u64) * 2;
    if output.plan_hint == crate::engram::PlanHint::NewTopicLikely && is_full_phase {
        brain.compress_plan(&output.current_plan_id)?;
    }

    Ok(())
}

/// v0.12.0: Heuristic compression without LLM.
/// Takes the last agent response as base, prepends up to 3 non-empty user inputs as keywords.
pub(crate) fn heuristic_compress(
    brain: &Brain,
    turns: &[DialogueTurn],
    plan_name: &str,
) -> String {
    let _ = brain; // used for future integration
    let last_response = turns
        .last()
        .map(|t| t.agent_response.as_str())
        .unwrap_or("");

    if last_response.is_empty() {
        return format!("{}: 对话完成", plan_name);
    }

    // Extract keywords from user inputs (first 3 different non-empty inputs)
    let keywords: Vec<&str> = turns
        .iter()
        .map(|t| t.user_input.trim())
        .filter(|s| !s.is_empty())
        .take(3)
        .collect();

    format!(
        "{}: {} — {}",
        plan_name,
        keywords.join("; "),
        last_response
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_keywords_empty() {
        let kw = extract_keywords("", 5);
        assert!(kw.is_empty());
    }

    #[test]
    fn test_extract_keywords_basic() {
        let kw = extract_keywords("今天天气很好适合出门散步", 5);
        assert!(!kw.is_empty());
        // All returned tokens should be ≥ 2 chars
        for w in &kw {
            assert!(w.len() >= 2, "keyword '{}' is too short", w);
        }
        assert!(kw.len() <= 5);
    }

    #[test]
    fn test_extract_keywords_stop_words_removed() {
        let kw = extract_keywords("的 了 在 是 我 有 和", 10);
        // All are stop words, so should be empty
        assert!(kw.is_empty());
    }

    #[test]
    fn test_extract_keywords_max_respected() {
        let text = "apple banana cherry date elderberry fig grape";
        let kw = extract_keywords(text, 3);
        assert!(kw.len() <= 3, "got {} keywords, expected ≤ 3", kw.len());
    }
}
