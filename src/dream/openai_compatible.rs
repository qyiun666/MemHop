// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

#![cfg(feature = "llm")]

//! OpenAI-compatible LLM Provider — consolidated dream consolidation.
//!
//! Two-phase architecture:
//!   1. `consolidate()` — one monolithic call processes L2 grouping, L3 extraction,
//!      habit analysis, and crystal generation in one prompt.
//!   2. `retry_sections()` — if any section fails parsing, a second call targets
//!      only the failed sections.

use crate::config::LlmConfig;
use crate::dream::llm::{
    ConsolidationInput, ConsolidationOutput, CrystalDef, CrystalStep, DreamSection, HabitAnalysis,
    L2Group, L3Extraction, LlmConcept, LlmProvider, LlmRelation, Section,
};
use crate::MemHopError;
use serde_json::json;
use std::collections::{HashMap, HashSet};

// ============================================================================
// System prompt & constants
// ============================================================================

const SYSTEM_CONSOLIDATE: &str = r#"你是 MemHop 认知记忆整合引擎（Cognitive Memory Consolidation Engine）。你的职责是对用户的对话记忆进行结构化整合，在一次调用中完成四个独立分析任务。

## 角色定位
你是记忆提炼专家，负责从非结构化对话中提取结构化知识。你必须：
- 严格遵循每个任务定义的 JSON 输出格式
- 只基于输入数据中的事实进行分析，不添加臆测
- 当输入数据不足以产生有意义的输出时，对应字段返回 null

## 通用质量标准
1. **精确性**：保留原文中所有专有名词、技术术语、版本号、数字，不得泛化或改写。
2. **双语保留**：中英文混合术语保留原文形态，例如 "JWT token" 不翻译，"React hooks" 保持英文。
3. **关键词优先**：使用技术关键词而非口语化表述，例如输出 "用户认证流程" 而非 "讨论了怎么登录"。
4. **可验证性**：每条提取的概念、关系、习惯必须能从输入文本中直接追溯到原文依据。
5. **去重对齐**：跨 context 的相同实体应在概念提取时合并，避免重复定义。

## 任务概览
你将在一次响应中完成以下四个任务，每个任务独立分析、独立输出：
1. L2 话题分组合并 — 检测同话题相邻节点，生成合并标题与摘要
2. L3 知识蒸馏 — 从上下文中提取结构化概念实体与语义关系
3. 用户习惯分析 — 从对话记录中提取词汇特征、风格标签、情绪模式
4. 行为结晶生成 — 从行动链中提取可复用的条件-动作规则

详细输出格式见各任务说明。最终响应必须为一个完整的 JSON 对象。"#;

const JSON_RETRY_HINT: &str =
    "你的上一次响应无法解析。请严格遵循输出格式，只返回有效的 JSON，不要包含 markdown 代码块标记或任何额外文字。";

// ============================================================================
// Provider struct
// ============================================================================

pub struct OpenAICompatibleLlmProvider {
    config: LlmConfig,
    client: reqwest::blocking::Client,
}

impl OpenAICompatibleLlmProvider {
    pub fn new(config: LlmConfig) -> Self {
        Self {
            config,
            client: reqwest::blocking::Client::new(),
        }
    }

    // ========================================================================
    // Low-level API call
    // ========================================================================

    fn call_api(
        &self,
        system: &str,
        user_prompt: &str,
        max_tokens: u32,
        temperature: f32,
        top_p: f32,
        presence_penalty: f32,
        frequency_penalty: f32,
    ) -> Result<String, MemHopError> {
        let body = json!({
            "model": self.config.model,
            "messages": [
                {"role": "system", "content": system},
                {"role": "user", "content": user_prompt},
            ],
            "max_tokens": max_tokens,
            "temperature": temperature,
            "top_p": top_p,
            "presence_penalty": presence_penalty,
            "frequency_penalty": frequency_penalty,
            "stream": false,
        });

        let response = self
            .client
            .post(&self.config.api_url)
            .bearer_auth(&self.config.api_key)
            .json(&body)
            .timeout(std::time::Duration::from_secs(self.config.timeout_secs))
            .send()
            .map_err(|e| MemHopError::EncoderError(format!("API call failed: {}", e)))?;

        if !response.status().is_success() {
            return Err(MemHopError::EncoderError(format!(
                "API request failed: {} - {}",
                response.status(),
                response.text().unwrap_or_default()
            )));
        }

        let json: serde_json::Value = response
            .json()
            .map_err(|e| MemHopError::Serialization(format!("Parse response failed: {}", e)))?;

        json["choices"][0]["message"]["content"]
            .as_str()
            .map(|s| s.to_string())
            .ok_or_else(|| MemHopError::Serialization("No content in response".to_string()))
    }

    /// Call with one retry on parse failure.
    fn call_with_retry<T>(
        &self,
        system: &str,
        user_prompt: &str,
        max_tokens: u32,
        temperature: f32,
        top_p: f32,
        presence_penalty: f32,
        frequency_penalty: f32,
        parse: &dyn Fn(&str) -> Result<T, MemHopError>,
    ) -> Result<T, MemHopError> {
        let response = self.call_api(
            system,
            user_prompt,
            max_tokens,
            temperature,
            top_p,
            presence_penalty,
            frequency_penalty,
        )?;
        if let Ok(val) = parse(&response) {
            return Ok(val);
        }

        // Retry once with hint
        let retry_prompt = format!("{}\n\n{}", user_prompt, JSON_RETRY_HINT);
        let response2 = self.call_api(
            system,
            &retry_prompt,
            max_tokens,
            temperature,
            top_p,
            presence_penalty,
            frequency_penalty,
        )?;
        parse(&response2)
    }

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

    // ========================================================================
    // Consolidation parameters
    // ========================================================================

    fn cons_temperature() -> f32 {
        0.15
    }
    fn cons_top_p() -> f32 {
        0.85
    }
    fn cons_presence_penalty() -> f32 {
        0.1
    }
    fn cons_frequency_penalty() -> f32 {
        0.05
    }
    fn cons_max_tokens() -> u32 {
        16384
    }
    fn retry_max_tokens() -> u32 {
        8192
    }

    // ========================================================================
    // Prompt builders
    // ========================================================================

    fn build_data_section(input: &ConsolidationInput) -> String {
        let mut s = String::new();

        s.push_str("## L2 上下文数据 (按 scene 分组，节点按时间排序)\n\n");
        for scene in &input.scenes {
            s.push_str(&format!("### scene_id = {}\n", scene.scene_id));
            for node in &scene.nodes {
                let title = if node.fused_keywords.is_empty() {
                    node.user_keywords.join(", ")
                } else {
                    node.fused_keywords.join(", ")
                };
                s.push_str(&format!(
                    "- id={:016x}  depth={}  user_kw={:?}  agent_kw={:?}  title=\"{}\"\n",
                    node.id_hash, node.depth, node.user_keywords, node.agent_keywords, title
                ));
                if let Some(ref fused) = node.fused_summary {
                    s.push_str(&format!(
                        "  fused_summary: {}\n",
                        fused.chars().take(400).collect::<String>()
                    ));
                }
            }
            s.push('\n');
        }

        if !input.recent_dialogues.is_empty() {
            s.push_str(&format!(
                "## 最近对话记录 ({} 条, 用于习惯分析)\n\n",
                input.recent_dialogues.len()
            ));
            for (i, d) in input.recent_dialogues.iter().enumerate() {
                s.push_str(&format!(
                    "{}. {}\n",
                    i + 1,
                    d.chars().take(300).collect::<String>()
                ));
            }
            s.push('\n');
        }

        if !input.existing_chains.is_empty() {
            s.push_str(&format!(
                "## 已有行动链 ({} 条, 用于结晶生成)\n\n",
                input.existing_chains.len()
            ));
            for c in &input.existing_chains {
                s.push_str(&format!(
                    "- title: \"{}\", trigger: \"{}\", count: {}, confidence: {:.2}\n",
                    c.title, c.trigger, c.trigger_count, c.confidence
                ));
            }
            s.push('\n');
        }
        s
    }

    fn build_task_prompt(sections: Option<&[DreamSection]>) -> String {
        let all = sections.is_none();
        let targets = sections.unwrap_or(&[
            DreamSection::L2Groups,
            DreamSection::L3Distill,
            DreamSection::Habits,
            DreamSection::Crystals,
        ]);
        let want = |sec: DreamSection| -> bool { targets.contains(&sec) };
        let mut s = String::from("# 任务\n\n各任务独立处理输入数据。每个任务独立输出。\n\n");

        if all || want(DreamSection::L2Groups) {
            s.push_str(r#"## 任务一：L2 话题压缩判断与执行

对每个 scene，分析其 depth-1 节点，判断是否需要压缩合并，如需则执行合并并标记级联更新。

步骤1 - 压缩判断：
对每个 scene 内 time-adjacent 的 depth-1 节点，判断是否满足合并条件：
- 条件A：相邻节点共享 ≥2 个相同关键词（从 title 或 fused_keywords 提取）
- 条件B：节点摘要内容语义相关（同一主题/同一任务的不同阶段）
- 条件C：节点时间跨度 < 24 小时
满足 (A 且 B) 或 (A 且 C) 则标记为待合并组。
每个 group ≥2 个连续节点。未被合并的节点不出现在输出中。

步骤2 - 合并执行：
对待合并的 group 生成：
- merged_title：合并后的话题标题（≤20字，覆盖组内所有节点的核心主题）
- merged_summary：合并摘要（100-200字，保留关键技术细节和决策结论）

步骤3 - 级联触发标记：
- 如果任何 scene 产生了合并 → 标记 l1_rebuild: true
- 如果 L1 被重建 → 标记 l0_rebuild: true

输出格式:
{"l2_groups": [{"scene_id": <u64>, "node_hashes": [<u64>,..], "merged_title": "<≤20字>", "merged_summary": "<100-200字>"}], "l2_compression_needed": <bool>, "l1_rebuild": <bool>, "l0_rebuild": <bool>}

空组时返回: {"l2_groups":[], "l2_compression_needed":false, "l1_rebuild":false, "l0_rebuild":false}
"#);
        }

        if all || want(DreamSection::L3Distill) {
            s.push_str(r#"## 任务二：L3 知识蒸馏

从每个 L2 context 的标题+摘要中提取结构化概念和关系。跨 context 做实体对齐去重。

输出格式:
{"l3_extractions": [{"context_id": <u64>, "concepts": [{"name":"<概念>","type":"concept|entity|skill|tool|version|framework","description":"<描述>","keywords":["<kw>"]}], "relations": [{"from":"<源>","to":"<目标>","kind":"Related|Causal|PartOf|Sequence|Dependency|Hierarchical|CoOccurrence"}]}]}

每个 context 最多 8 概念 + 12 条关系。无内容时 context 可不出现。
"#);
        }

        if all || want(DreamSection::Habits) {
            s.push_str(r#"## 任务三：用户习惯分析

从对话记录提取用户特征：词典(≤10条)、风格标签(≤5个)、情绪模式(≤5条)。

输出格式:
{"habits": {"lexicon":{"<用词>":"<含义>"},"style_traits":["<标签>"],"emotion_patterns":{"<表达>":"<情绪含义>"}}}

无对话数据时返回 null。
"#);
        }

        if all || want(DreamSection::Crystals) {
            s.push_str(r#"## 任务四：行为结晶生成

从行动链提取可复用规则，生成结构化结晶。

输出格式:
{"crystals": [{"condition":"<DSL>","action":"<动作>","steps":[{"action":"<步骤>","parameters":"<JSON|null>"}],"confidence":0.0-1.0}]}

DSL 支持: == != > < >= <= AND OR NOT
无数据时返回 []。
"#);
        }

        s.push_str("\n# 最终输出\n\n合并为单一 JSON，每字段独立可为 null:\n{\"l2_groups\":[...], \"l2_compression_needed\":bool, \"l1_rebuild\":bool, \"l0_rebuild\":bool, \"l3_extractions\":[...], \"habits\":{...}, \"crystals\":[...]}");
        s
    }

    // ========================================================================
    // Parsers
    // ========================================================================

    fn parse_l2_groups(json: &serde_json::Value) -> Result<Vec<L2Group>, MemHopError> {
        let arr = json
            .as_array()
            .ok_or_else(|| MemHopError::Serialization("l2_groups: not array".into()))?;
        let mut groups = Vec::new();
        for item in arr {
            let scene_id = item["scene_id"].as_u64().unwrap_or(0);
            let node_hashes: Vec<u64> = item["node_hashes"]
                .as_array()
                .map(|a| a.iter().filter_map(|v| v.as_u64()).collect())
                .unwrap_or_default();
            let merged_title = item["merged_title"].as_str().unwrap_or("").to_string();
            let merged_summary = item["merged_summary"].as_str().unwrap_or("").to_string();
            if node_hashes.len() >= 2 && !merged_title.is_empty() {
                groups.push(L2Group {
                    scene_id,
                    node_hashes,
                    merged_title,
                    merged_summary,
                });
            }
        }
        Ok(groups)
    }

    fn parse_l3_extractions(json: &serde_json::Value) -> Result<Vec<L3Extraction>, MemHopError> {
        let arr = json
            .as_array()
            .ok_or_else(|| MemHopError::Serialization("l3_extractions: not array".into()))?;
        let mut out = Vec::new();
        for item in arr {
            let context_id = item["context_id"].as_u64().unwrap_or(0);
            let concepts: Vec<LlmConcept> = item["concepts"]
                .as_array()
                .map(|a| {
                    a.iter()
                        .flat_map(|c| serde_json::from_value(c.clone()))
                        .collect()
                })
                .unwrap_or_default();
            let relations: Vec<LlmRelation> = item["relations"]
                .as_array()
                .map(|a| {
                    a.iter()
                        .flat_map(|r| serde_json::from_value(r.clone()))
                        .collect()
                })
                .unwrap_or_default();
            if !concepts.is_empty() {
                out.push(L3Extraction {
                    context_id,
                    concepts,
                    relations,
                });
            }
        }
        Ok(out)
    }

    fn parse_habits(json: &serde_json::Value) -> Result<HabitAnalysis, MemHopError> {
        let mut a = HabitAnalysis::default();
        if let Some(obj) = json["lexicon"].as_object() {
            for (k, v) in obj {
                if let Some(m) = v.as_str() {
                    a.lexicon.insert(k.clone(), m.to_string());
                }
            }
        }
        if let Some(arr) = json["style_traits"].as_array() {
            for v in arr {
                if let Some(s) = v.as_str() {
                    a.style_traits.push(s.to_string());
                }
            }
        }
        if let Some(obj) = json["emotion_patterns"].as_object() {
            for (k, v) in obj {
                if let Some(m) = v.as_str() {
                    a.emotion_patterns.insert(k.clone(), m.to_string());
                }
            }
        }
        Ok(a)
    }

    fn parse_crystals(json: &serde_json::Value) -> Result<Vec<CrystalDef>, MemHopError> {
        let arr = json
            .as_array()
            .ok_or_else(|| MemHopError::Serialization("crystals: not array".into()))?;
        let mut out = Vec::new();
        for item in arr {
            let condition = item["condition"].as_str().unwrap_or("").to_string();
            let action = item["action"].as_str().unwrap_or("").to_string();
            let confidence = item["confidence"].as_f64().unwrap_or(0.5) as f32;
            let steps: Vec<CrystalStep> = item["steps"]
                .as_array()
                .map(|a| {
                    a.iter()
                        .map(|s| CrystalStep {
                            action: s["action"].as_str().unwrap_or("").to_string(),
                            parameters: s["parameters"].as_str().map(|p| p.to_string()),
                        })
                        .collect()
                })
                .unwrap_or_default();
            if !condition.is_empty() && !action.is_empty() {
                out.push(CrystalDef {
                    condition,
                    action,
                    steps,
                    confidence,
                });
            }
        }
        Ok(out)
    }

    fn parse_consolidated_response(
        response: &str,
        sections: Option<&[DreamSection]>,
    ) -> Result<ConsolidationOutput, MemHopError> {
        let all = sections.is_none();
        let targets = sections.unwrap_or(&[
            DreamSection::L2Groups,
            DreamSection::L3Distill,
            DreamSection::Habits,
            DreamSection::Crystals,
        ]);
        let want = |s: DreamSection| -> bool { targets.contains(&s) };
        let cleaned = Self::strip_code_blocks(response);
        let root: serde_json::Value = serde_json::from_str(&cleaned)
            .map_err(|e| MemHopError::Serialization(format!("Parse JSON: {}", e)))?;

        macro_rules! parse_or_empty {
            ($key:expr, $parser:expr, $wanted:expr, $ty:ty) => {{
                if !$wanted {
                    Section::Empty
                } else {
                    match root.get($key).filter(|v| !v.is_null()) {
                        Some(v) => match $parser(v) {
                            Ok(r) => Section::Valid(r),
                            Err(e) => {
                                tracing::warn!("section {}: {}", $key, e);
                                Section::ParseFailed(e.to_string())
                            }
                        },
                        None => Section::Empty,
                    }
                }
            }};
        }

        Ok(ConsolidationOutput {
            l2_groups: parse_or_empty!(
                "l2_groups",
                Self::parse_l2_groups,
                all || want(DreamSection::L2Groups),
                Vec<L2Group>
            ),
            l3_extractions: parse_or_empty!(
                "l3_extractions",
                Self::parse_l3_extractions,
                all || want(DreamSection::L3Distill),
                Vec<L3Extraction>
            ),
            habits: parse_or_empty!(
                "habits",
                Self::parse_habits,
                all || want(DreamSection::Habits),
                HabitAnalysis
            ),
            crystals: parse_or_empty!(
                "crystals",
                Self::parse_crystals,
                all || want(DreamSection::Crystals),
                Vec<CrystalDef>
            ),
        })
    }

    fn fallback_summarize_inner(texts: &[String]) -> (String, String) {
        let combined: String = texts
            .iter()
            .map(|t| t.chars().take(300).collect::<String>())
            .collect::<Vec<_>>()
            .join(" | ");
        let mut keywords: HashMap<String, usize> = HashMap::new();
        for text in texts {
            for word in crate::index::sparse::tokenize(text) {
                if word.len() > 1 {
                    *keywords.entry(word).or_insert(0) += 1;
                }
            }
        }
        let mut sorted: Vec<_> = keywords.into_iter().collect();
        sorted.sort_by_key(|b| std::cmp::Reverse(b.1));
        let kw_str: String = sorted
            .iter()
            .take(8)
            .map(|(k, _)| k.as_str())
            .collect::<Vec<_>>()
            .join(", ");
        let title = combined.chars().take(20).collect::<String>();
        (title, format!("[fallback] {}", kw_str))
    }

    fn fallback_habits_inner(dialogues: &[String]) -> HabitAnalysis {
        let stop: HashSet<&str> = [
            "the", "a", "an", "is", "are", "was", "were", "be", "to", "of", "in", "for", "on",
            "with", "at", "by", "and", "but", "or", "not", "的", "了", "在", "是", "我", "有",
            "和", "就", "不", "也", "很", "都",
        ]
        .iter()
        .copied()
        .collect();
        let mut freq: HashMap<String, usize> = HashMap::new();
        for text in dialogues {
            for word in text.split_whitespace() {
                let l = word.to_lowercase();
                if l.len() > 1 && !stop.contains(l.as_str()) {
                    *freq.entry(l).or_insert(0) += 1;
                }
            }
        }
        let mut sorted: Vec<_> = freq.into_iter().collect();
        sorted.sort_by_key(|b| std::cmp::Reverse(b.1));
        let lexicon: HashMap<String, String> = sorted
            .iter()
            .take(8)
            .map(|(w, c)| (w.clone(), format!("高频词(出现{}次)", c)))
            .collect();
        HabitAnalysis {
            lexicon,
            style_traits: vec![],
            emotion_patterns: HashMap::new(),
        }
    }
}

// ============================================================================
// Trait implementation
// ============================================================================

impl LlmProvider for OpenAICompatibleLlmProvider {
    fn consolidate(&self, input: &ConsolidationInput) -> Result<ConsolidationOutput, MemHopError> {
        let data = Self::build_data_section(input);
        let tasks = Self::build_task_prompt(None);
        let user_prompt = format!("# 输入数据\n\n{}\n\n{}", data, tasks);
        tracing::info!(
            "consolidate: scenes={} dialogues={} chains={} prompt_len={}",
            input.scenes.len(),
            input.recent_dialogues.len(),
            input.existing_chains.len(),
            user_prompt.len()
        );

        self.call_with_retry(
            SYSTEM_CONSOLIDATE,
            &user_prompt,
            Self::cons_max_tokens(),
            Self::cons_temperature(),
            Self::cons_top_p(),
            Self::cons_presence_penalty(),
            Self::cons_frequency_penalty(),
            &|r| Self::parse_consolidated_response(r, None),
        )
        .or_else(|e| {
            tracing::warn!("consolidate failed: {}", e);
            let err = format!("LLM error: {}", e);
            Ok(ConsolidationOutput {
                l2_groups: Section::ParseFailed(err.clone()),
                l3_extractions: Section::ParseFailed(err.clone()),
                habits: Section::ParseFailed(err.clone()),
                crystals: Section::ParseFailed(err.clone()),
            })
        })
    }

    fn retry_sections(
        &self,
        input: &ConsolidationInput,
        sections: &[DreamSection],
    ) -> Result<ConsolidationOutput, MemHopError> {
        if sections.is_empty() {
            return Ok(ConsolidationOutput {
                l2_groups: Section::Empty,
                l3_extractions: Section::Empty,
                habits: Section::Empty,
                crystals: Section::Empty,
            });
        }
        let names: Vec<&str> = sections
            .iter()
            .map(|s| match s {
                DreamSection::L2Groups => "L2",
                DreamSection::L3Distill => "L3",
                DreamSection::Habits => "habits",
                DreamSection::Crystals => "crystals",
            })
            .collect();
        let data = Self::build_data_section(input);
        let tasks = Self::build_task_prompt(Some(sections));
        let user_prompt = format!(
            "# 第二阶段重试\n仅处理: {}\n其他返回 null\n\n{}\n\n{}",
            names.join(","),
            data,
            tasks
        );
        tracing::info!(
            "retry: sections={:?} prompt_len={}",
            names,
            user_prompt.len()
        );

        self.call_with_retry(
            SYSTEM_CONSOLIDATE,
            &user_prompt,
            Self::retry_max_tokens(),
            Self::cons_temperature(),
            Self::cons_top_p(),
            Self::cons_presence_penalty(),
            Self::cons_frequency_penalty(),
            &|r| Self::parse_consolidated_response(r, Some(sections)),
        )
        .or_else(|e| {
            tracing::warn!("retry failed: {}", e);
            let err = format!("retry: {}", e);
            Ok(ConsolidationOutput {
                l2_groups: Section::ParseFailed(err.clone()),
                l3_extractions: Section::ParseFailed(err.clone()),
                habits: Section::ParseFailed(err.clone()),
                crystals: Section::ParseFailed(err.clone()),
            })
        })
    }

    fn fallback_summarize(&self, texts: &[String]) -> (String, String) {
        Self::fallback_summarize_inner(texts)
    }
    fn fallback_habits(&self, dialogues: &[String]) -> HabitAnalysis {
        Self::fallback_habits_inner(dialogues)
    }

    fn chat(
        &self,
        system: &str,
        user: &str,
        max_tokens: u32,
        temperature: f32,
        top_p: f32,
        presence_penalty: f32,
        frequency_penalty: f32,
    ) -> Result<String, MemHopError> {
        self.call_api(
            system,
            user,
            max_tokens,
            temperature,
            top_p,
            presence_penalty,
            frequency_penalty,
        )
    }
}
