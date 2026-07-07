// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

#![cfg(feature = "llm")]

//! OpenAI-compatible LLM Provider for any chat completions API.

use crate::config::LlmConfig;
use crate::dream::llm::{
    CompressedSummary, CrystalDef, CrystalStep, HabitAnalysis, LlmDistillResult, LlmProvider,
    MemorySummary, Pattern,
};
use crate::MemHopError;
use serde_json::json;
use std::collections::{HashMap, HashSet};

const SYSTEM_SUMMARIZE: &str =
    "你是 MemHop 记忆压缩专家。请将多轮对话压缩为关键词密集的摘要，用于后续检索。\n\n\
     要求:\n\
     1. 提取核心主题(2-5个关键词，用空格分隔)\n\
     2. 提取关键信息点(3-8条，每条一个完整信息)\n\
     3. 生成摘要段落(100-200字，包含所有关键信息，用关键词而非完整句子)\n\n\
     摘要必须:\n\
     - 保留所有专有名词(人名、地名、技术术语、版本号)\n\
     - 保留所有数字和日期\n\
     - 用关键词而非口语化表述(如\"用户认证\"而非\"讨论了怎么登录\")\n\
     - 中英文术语保留原文(如\"JWT\"不翻译)";
const SYSTEM_DISTILL: &str = "你是 MemHop 知识蒸馏引擎。请从摘要中提取结构化知识图谱。\n\n\
     提取规则:\n
     1. 概念: 只提取有明确定义的实体/技术/概念，不超过 10 个\n
     2. 每个概念必须有 keywords(用于 BM25 检索)\n
     3. 关系: 只提取有明确逻辑关系的概念对，不超过 15 条\n
     4. 去重: 相同含义的概念合并为一个\n
     5. 关系 kind: Related(相关)、Causal(因果)、PartOf(部分-整体)、\n
        Sequence(顺序)、Dependency(依赖)、Hierarchical(层级)、CoOccurrence(共现)";
const SYSTEM_CRYSTAL: &str = "你是 MemHop 技能结晶系统，擅长从动作模式中提取可复用技能。请分析输入的行为模式，生成结构化的技能定义。";
const SYSTEM_HABITS: &str =
    "你是用户语言习惯分析专家，擅长从对话记录中识别用户的独特语言模式和沟通风格。";
const SYSTEM_PATTERNS: &str =
    "你是 MemHop 行为模式分析专家，擅长从历史记忆中识别重复出现的行为规律。";
const JSON_RETRY_MESSAGE: &str = "请返回纯JSON格式，不要包含markdown代码块标记或任何额外文字。";

/// OpenAI-compatible LLM provider
///
/// Works with any API that follows the OpenAI chat completions format.
/// Configure with your preferred provider's API key, endpoint URL, and model name.
pub struct OpenAICompatibleLlmProvider {
    /// LLM configuration (model, endpoint, temperature, timeout, ...)
    config: LlmConfig,
    /// HTTP client for API calls
    client: reqwest::blocking::Client,
}

impl OpenAICompatibleLlmProvider {
    /// Create a new LLM provider from configuration.
    pub fn new(config: LlmConfig) -> Self {
        Self {
            config,
            client: reqwest::blocking::Client::new(),
        }
    }

    /// Call the OpenAI-compatible API with a complete message list
    fn call_api_messages(
        &self,
        messages: &[serde_json::Value],
        max_tokens: u32,
        params: Option<&crate::layers::context::LlmParams>,
    ) -> Result<String, MemHopError> {
        let temperature = params
            .map(|p| p.temperature)
            .unwrap_or(self.config.temperature);
        let top_p = params.map(|p| p.top_p).unwrap_or(self.config.top_p);
        let presence_penalty = params
            .map(|p| p.presence_penalty)
            .unwrap_or(self.config.presence_penalty);
        let frequency_penalty = params
            .map(|p| p.frequency_penalty)
            .unwrap_or(self.config.frequency_penalty);

        let body = json!({
            "model": self.config.model,
            "messages": messages,
            "max_tokens": max_tokens,
            "temperature": temperature,
            "top_p": top_p,
            "presence_penalty": presence_penalty,
            "frequency_penalty": frequency_penalty,
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

    /// Call the API and retry once with a JSON-format reminder if parsing fails.
    fn call_api_json<T, F>(
        &self,
        system: &str,
        user_prompt: &str,
        max_tokens: u32,
        params: Option<&crate::layers::context::LlmParams>,
        parse: F,
    ) -> Result<T, MemHopError>
    where
        F: Fn(&str) -> Result<T, MemHopError>,
    {
        let mut messages = vec![
            json!({"role": "system", "content": system}),
            json!({"role": "user", "content": user_prompt}),
        ];

        let response = self.call_api_messages(&messages, max_tokens, params)?;
        if let Ok(value) = parse(&response) {
            return Ok(value);
        }

        messages.push(json!({"role": "user", "content": JSON_RETRY_MESSAGE}));
        let response = self.call_api_messages(&messages, max_tokens, params)?;
        parse(&response)
    }

    /// Default parameters for each dream stage function.
    /// These are tuned for memory-specific tasks (not general chat).
    fn params_for_summarize() -> crate::layers::context::LlmParams {
        // High determinism: we need consistent, factual compression
        crate::layers::context::LlmParams {
            temperature: 0.1,
            top_p: 0.85,
            presence_penalty: 0.0,
            frequency_penalty: 0.0,
        }
    }

    fn params_for_distill() -> crate::layers::context::LlmParams {
        // Very high determinism: structured JSON must be parseable
        crate::layers::context::LlmParams {
            temperature: 0.0,
            top_p: 0.8,
            presence_penalty: 0.2,
            frequency_penalty: 0.1,
        }
    }

    fn params_for_crystal() -> crate::layers::context::LlmParams {
        // Moderate creativity for DSL generation, but still structured
        crate::layers::context::LlmParams {
            temperature: 0.2,
            top_p: 0.9,
            presence_penalty: 0.1,
            frequency_penalty: 0.0,
        }
    }

    fn params_for_habits() -> crate::layers::context::LlmParams {
        // Balanced: need to detect patterns but not hallucinate
        crate::layers::context::LlmParams {
            temperature: 0.15,
            top_p: 0.88,
            presence_penalty: 0.1,
            frequency_penalty: 0.05,
        }
    }

    fn params_for_patterns() -> crate::layers::context::LlmParams {
        // Similar to habits: pattern detection needs consistency
        crate::layers::context::LlmParams {
            temperature: 0.15,
            top_p: 0.88,
            presence_penalty: 0.1,
            frequency_penalty: 0.05,
        }
    }

    /// Strip markdown code block markers from a JSON response if present.
    fn strip_code_blocks(response: &str) -> String {
        let trimmed = response.trim();
        if let Some(stripped) = trimmed.strip_prefix("```") {
            // 跳过第一行（可能是 ```json 等语言标记）
            let start = stripped.find('\n').map(|i| i + 1).unwrap_or(0);
            let rest = &stripped[start..];
            // 在剩余文本中搜索闭合的 ```
            let end = rest.rfind("```").unwrap_or(rest.len());
            rest[..end].trim().to_string()
        } else {
            trimmed.to_string()
        }
    }
}

impl LlmProvider for OpenAICompatibleLlmProvider {
    fn summarize(&self, texts: &[String]) -> Result<CompressedSummary, MemHopError> {
        let memories_text = texts
            .iter()
            .enumerate()
            .map(|(i, t)| format!("{}. {}", i + 1, t))
            .collect::<Vec<_>>()
            .join("\n");

        let user_prompt = format!(
            "# 上下文信息\n\
             {memories_text}\n\n\
             # 任务\n\
             请将以上内容压缩为结构化摘要。\n\n\
             # 输出格式(JSON)\n\
             {{\n\
               \"theme\": \"核心主题关键词(空格分隔,2-5个)\",\n\
               \"title\": \"压缩后的简短标题(不超过20字)\",\n\
               \"key_points\": [\"关键信息点1\", \"关键信息点2\"],\n\
               \"summary\": \"关键词密集的摘要段落(100-200字)\"\n\
             }}\n"
        );

        self.call_api_json(
            SYSTEM_SUMMARIZE,
            &user_prompt,
            1024, // summarize: more tokens for richer output
            Some(&Self::params_for_summarize()),
            |response| {
                let cleaned = Self::strip_code_blocks(response);
                let json: serde_json::Value = serde_json::from_str(&cleaned).map_err(|e| {
                    MemHopError::Serialization(format!("Parse summary failed: {}", e))
                })?;
                let theme = json["theme"].as_str().unwrap_or("").to_string();
                let title = json["title"].as_str().unwrap_or("").to_string();
                let key_points = json["key_points"]
                    .as_array()
                    .map(|arr| {
                        arr.iter()
                            .filter_map(|v| v.as_str().map(String::from))
                            .collect()
                    })
                    .unwrap_or_default();
                let summary = json["summary"]
                    .as_str()
                    .map(|s| s.to_string())
                    .ok_or_else(|| {
                        MemHopError::Serialization("Missing summary field".to_string())
                    })?;
                Ok(CompressedSummary {
                    theme,
                    title,
                    key_points,
                    summary,
                })
            },
        )
        .or_else(|e| {
            tracing::warn!("LLM summarize failed, using fallback: {}", e);
            Ok(self.fallback_summarize(texts))
        })
    }

    fn extract_patterns(&self, memories: &[MemorySummary]) -> Result<Vec<Pattern>, MemHopError> {
        let memories_text = memories
            .iter()
            .enumerate()
            .map(|(i, m)| {
                format!(
                    "{}. [{}] {}\n   Keywords: {}",
                    i + 1,
                    chrono::DateTime::from_timestamp_millis(m.timestamp)
                        .map(|dt| dt.format("%Y-%m-%d").to_string())
                        .unwrap_or_else(|| "unknown".to_string()),
                    m.text,
                    m.keywords.join(", ")
                )
            })
            .collect::<Vec<_>>()
            .join("\n");

        let user_prompt = format!(
            "# 任务\n\
             分析以下记忆条目，提取重复出现的行为模式。重点关注:\n\
             - 高频出现的动作或决策\n\
             - 相似场景下的反应模式\n\
             - 时间或情境相关的规律\n\n\
             # 输出格式\n\
             返回JSON数组，每个对象包含:\n\
             - description: string (模式描述，清晰简洁)\n\
             - frequency: integer (出现频率，1-100)\n\
             - confidence: float (置信度，0.0-1.0)\n\n\
             # 示例\n\
             [\n\
               {{\"description\": \"每周三晚上学习新技术\" , \"frequency\": 85, \"confidence\": 0.9}},\n\
               {{\"description\": \"遇到问题时先查阅文档再求助\" , \"frequency\": 70, \"confidence\": 0.8}}\n\
             ]\n\n\
             # 输入数据\n\
             {memories_text}\n\n\
             # 开始分析\n"
        );

        self.call_api_json(
            SYSTEM_PATTERNS,
            &user_prompt,
            1024, // pattern extraction
            Some(&Self::params_for_patterns()),
            |response| {
                let cleaned = Self::strip_code_blocks(response);
                let patterns: Vec<serde_json::Value> =
                    serde_json::from_str(&cleaned).map_err(|e| {
                        MemHopError::Serialization(format!("Parse patterns failed: {}", e))
                    })?;
                Ok(patterns
                    .into_iter()
                    .map(|p| Pattern {
                        description: p["description"].as_str().unwrap_or("").to_string(),
                        frequency: p["frequency"].as_u64().unwrap_or(1) as u32,
                        confidence: p["confidence"].as_f64().unwrap_or(0.5) as f32,
                    })
                    .collect())
            },
        )
        .or_else(|e| {
            tracing::warn!("LLM extract_patterns failed, using fallback: {}", e);
            Ok(self.fallback_extract_patterns(memories))
        })
    }

    fn generate_crystal(&self, pattern: &Pattern) -> Result<CrystalDef, MemHopError> {
        let user_prompt = format!(
            "# 任务\n\
             基于以下行为模式，生成一个可执行的结晶规则(DSL格式)。\n\
             规则应包含:\n\
             1. condition: 触发条件(使用DSL语法，如 \"time.weekday == 3 AND time.hour >= 20\")\n\
             2. action: 总体执行动作(简洁明了的操作指令)\n\
             3. steps: 有序步骤数组，每个步骤包含 action 和可选的 parameters (JSON字符串)\n\
             4. confidence: 规则的置信度(0.0-1.0)\n\n\
             # DSL语法参考\n\
             - 时间条件: time.weekday (0-6), time.hour (0-23), time.minute\n\
             - 比较操作: ==, !=, >, <, >=, <=\n\
             - 逻辑操作: AND, OR, NOT\n\
             - 示例: \"time.weekday == 3 AND time.hour >= 20 AND context.location == 'home'\"\n\n\
             # 输出格式\n\
             返回JSON对象:\n\
             {{\n\
               \"condition\": \"DSL格式条件\",\n\
               \"action\": \"总体执行动作\",\n\
               \"steps\": [\n\
                 {{\"action\": \"步骤1动作\", \"parameters\": \"{{\\\"key\\\":\\\"value\\\"}}\"}},\n\
                 {{\"action\": \"步骤2动作\", \"parameters\": null}}\n\
               ],\n\
               \"confidence\": 0.85\n\
             }}\n\n\
             # 输入数据\n\
             模式描述: {description}\n\
             出现频率: {frequency}\n\
             基础置信度: {base_confidence:.2}\n\n\
             # 开始生成\n",
            description = pattern.description,
            frequency = pattern.frequency,
            base_confidence = pattern.confidence
        );

        self.call_api_json(
            SYSTEM_CRYSTAL,
            &user_prompt,
            1024, // crystal with steps
            Some(&Self::params_for_crystal()),
            |response| {
                let cleaned = Self::strip_code_blocks(response);
                let json: serde_json::Value = serde_json::from_str(&cleaned).map_err(|e| {
                    MemHopError::Serialization(format!("Parse crystal failed: {}", e))
                })?;

                let condition = json["condition"].as_str().unwrap_or("").to_string();
                let action = json["action"].as_str().unwrap_or("").to_string();
                let confidence = json["confidence"]
                    .as_f64()
                    .unwrap_or(pattern.confidence as f64) as f32;

                let steps = if let Some(arr) = json["steps"].as_array() {
                    let parsed: Vec<CrystalStep> = arr
                        .iter()
                        .map(|s| CrystalStep {
                            action: s["action"].as_str().unwrap_or("").to_string(),
                            parameters: s["parameters"].as_str().map(|p| p.to_string()),
                        })
                        .collect();
                    if parsed.is_empty() {
                        vec![CrystalStep {
                            action: action.clone(),
                            parameters: None,
                        }]
                    } else {
                        parsed
                    }
                } else {
                    vec![CrystalStep {
                        action: action.clone(),
                        parameters: None,
                    }]
                };

                Ok(CrystalDef {
                    condition,
                    action,
                    steps,
                    confidence,
                })
            },
        )
        .or_else(|e| {
            tracing::warn!("LLM generate_crystal failed, using fallback: {}", e);
            Ok(self.fallback_generate_crystal(pattern))
        })
    }

    fn fallback_summarize(&self, texts: &[String]) -> CompressedSummary {
        // Use the project's unified tokenizer (jieba + camelCase) for CJK-aware
        // keyword extraction, instead of simple split_whitespace.
        let mut keyword_freq: HashMap<String, usize> = HashMap::new();
        let mut all_text = String::new();
        for text in texts {
            all_text.push_str(text);
            all_text.push(' ');
            for word in crate::index::sparse::tokenize(text) {
                if word.len() > 1 {
                    *keyword_freq.entry(word).or_insert(0) += 1;
                }
            }
        }

        let mut sorted: Vec<_> = keyword_freq.into_iter().collect();
        sorted.sort_by_key(|b| std::cmp::Reverse(b.1));

        let keywords: Vec<String> = sorted.into_iter().take(15).map(|(k, _)| k).collect();
        let theme = keywords.get(0..3).unwrap_or(&[]).join(" ");
        let summary = keywords.join(", ");
        CompressedSummary {
            theme,
            title: all_text.chars().take(20).collect(),
            key_points: Vec::new(),
            summary,
        }
    }

    fn fallback_extract_patterns(&self, memories: &[MemorySummary]) -> Vec<Pattern> {
        if memories.is_empty() {
            return vec![];
        }

        let mut keyword_count: HashMap<String, usize> = HashMap::new();
        for memory in memories {
            let mut seen = HashSet::new();
            for kw in &memory.keywords {
                if seen.insert(kw.clone()) {
                    *keyword_count.entry(kw.clone()).or_insert(0) += 1;
                }
            }
        }

        let total = memories.len();
        let common_keywords: Vec<_> = keyword_count
            .into_iter()
            .filter(|(_, count)| *count >= 2) // 至少出现在2个记忆中
            .map(|(kw, count)| Pattern {
                description: format!("Common theme: {}", kw),
                frequency: count as u32,
                confidence: count as f32 / total as f32,
            })
            .collect();

        common_keywords
    }

    fn fallback_generate_crystal(&self, pattern: &Pattern) -> CrystalDef {
        let re = regex::Regex::new(r"(?i)(?:when|if)\s+(.+?)\s*(?:then|→|=>)\s*(.+)").unwrap();

        if let Some(caps) = re.captures(&pattern.description) {
            let action = caps[2].trim().to_string();
            CrystalDef {
                condition: caps[1].trim().to_string(),
                action: action.clone(),
                steps: vec![CrystalStep {
                    action,
                    parameters: None,
                }],
                confidence: 0.5,
            }
        } else {
            CrystalDef {
                condition: format!(
                    "trigger: {}",
                    pattern.description.chars().take(50).collect::<String>()
                ),
                action: "log_and_notify".to_string(),
                steps: vec![CrystalStep {
                    action: "执行默认动作".to_string(),
                    parameters: None,
                }],
                confidence: 0.3,
            }
        }
    }

    fn analyze_user_habits(&self, dialogues: &[String]) -> Result<HabitAnalysis, MemHopError> {
        let dialogues_text = dialogues
            .iter()
            .enumerate()
            .map(|(i, d)| format!("{}. {}", i + 1, d))
            .collect::<Vec<_>>()
            .join("\n");

        let user_prompt = format!(
            "# 任务\n\
             分析以下用户的对话记录，提取三个维度的信息:\n\n\
             ## 1. 用户词典 (lexicon)\n\
             识别用户独特的用词习惯，包括:\n\
             - 网络用语/俚语及其含义 (如 \"6\"→\"厉害/牛\"， \"摸鱼\"→\"偷懒休息\")\n\
             - 用户自创的缩写或术语\n\
             - 有个人特色的表达方式\n\
             最多提取15条，每条包含用词和含义。\n\n\
             ## 2. 沟通风格 (style_traits)\n\
             识别用户的沟通风格特征，用英文标签表示，例如:\n\
             - \"prefers_brevity\" (喜欢简短回答)\n\
             - \"uses_casual_tone\" (语气随意)\n\
             - \"likes_code_examples\" (喜欢代码示例)\n\
             - \"asks_follow_up\" (经常追问)\n\
             - \"uses_humor\" (幽默风格)\n\
             最多5个标签。\n\n\
             ## 3. 情绪表达模式 (emotion_patterns)\n\
             识别用户独特的情绪表达方式，包括:\n\
             - 特定词汇/表情代表的情绪 (如 \"呵呵\"→\"不满或敷衍\")\n\
             - 语气词的含义\n\
             最多5条。\n\n\
             # 输出格式\n\
             返回严格JSON格式:\n\
             {{\n\
               \"lexicon\": {{\"用词\": \"含义\", ...}},\n\
               \"style_traits\": [\"trait1\", \"trait2\"],\n\
               \"emotion_patterns\": {{\"表达\": \"含义\", ...}}\n\
             }}\n\n\
             # 输入数据\n\
             {dialogues_text}\n\n\
             # 开始分析\n"
        );

        self.call_api_json(
            SYSTEM_HABITS,
            &user_prompt,
            1024, // habits
            Some(&Self::params_for_habits()),
            |response| {
                let cleaned = Self::strip_code_blocks(response);
                let json: serde_json::Value = serde_json::from_str(&cleaned).map_err(|e| {
                    MemHopError::Serialization(format!("Parse habit analysis failed: {}", e))
                })?;

                let mut lexicon = HashMap::new();
                if let Some(obj) = json["lexicon"].as_object() {
                    for (k, v) in obj {
                        if let Some(meaning) = v.as_str() {
                            lexicon.insert(k.clone(), meaning.to_string());
                        }
                    }
                }

                let mut style_traits = Vec::new();
                if let Some(arr) = json["style_traits"].as_array() {
                    for v in arr {
                        if let Some(s) = v.as_str() {
                            style_traits.push(s.to_string());
                        }
                    }
                }

                let mut emotion_patterns = HashMap::new();
                if let Some(obj) = json["emotion_patterns"].as_object() {
                    for (k, v) in obj {
                        if let Some(meaning) = v.as_str() {
                            emotion_patterns.insert(k.clone(), meaning.to_string());
                        }
                    }
                }

                Ok(HabitAnalysis {
                    lexicon,
                    style_traits,
                    emotion_patterns,
                })
            },
        )
        .or_else(|e| {
            tracing::warn!("LLM analyze_user_habits failed, using fallback: {}", e);
            Ok(self.fallback_analyze_user_habits(dialogues))
        })
    }

    fn fallback_analyze_user_habits(&self, dialogues: &[String]) -> HabitAnalysis {
        // Simple fallback: extract high-frequency non-stop words as lexicon candidates
        let stop_words: HashSet<&str> = [
            "the", "a", "an", "is", "are", "was", "were", "be", "been", "have", "has", "had", "do",
            "does", "did", "will", "would", "could", "should", "may", "might", "can", "shall",
            "to", "of", "in", "for", "on", "with", "at", "by", "from", "as", "into", "through",
            "during", "and", "but", "or", "not", "no", "nor", "so", "yet", "both", "either",
            "neither", "each", "every", "all", "any", "few", "more", "most", "other", "some",
            "such", "than", "too", "very", "just", "because", "if", "when", "while", "的", "了",
            "在", "是", "我", "有", "和", "就", "不", "人", "都", "一", "一个", "上", "也", "很",
            "到", "说", "要", "去", "你", "会", "着", "没有", "看", "好", "自己", "这",
        ]
        .iter()
        .copied()
        .collect();

        let mut word_freq: HashMap<String, usize> = HashMap::new();
        for text in dialogues {
            for word in text.split_whitespace() {
                let lower = word.to_lowercase();
                if lower.len() > 1 && !stop_words.contains(lower.as_str()) {
                    *word_freq.entry(lower).or_insert(0) += 1;
                }
            }
        }

        let mut sorted: Vec<_> = word_freq.into_iter().collect();
        sorted.sort_by_key(|b| std::cmp::Reverse(b.1));

        let mut lexicon = HashMap::new();
        for (word, freq) in sorted.into_iter().take(10) {
            lexicon.insert(word.clone(), format!("高频词(出现{}次)", freq));
        }

        HabitAnalysis {
            lexicon,
            style_traits: Vec::new(),
            emotion_patterns: HashMap::new(),
        }
    }

    fn distill_concepts(&self, summary: &str) -> Result<LlmDistillResult, MemHopError> {
        let user_prompt = format!(
            "# 任务\n\
             从以下摘要提取知识图谱。\n\n\
             # 摘要内容\n{summary}\n\n\
             # 输出格式(严格JSON)\n\
             {{\n\
               \"concepts\": [\n\
                 {{\"name\": \"概念名\", \"type\": \"concept|entity|skill|tool|version\", \"description\": \"概念描述\", \"keywords\": [\"关键词1\", \"关键词2\"]}}\n\
               ],\n\
               \"relations\": [\n\
                 {{\"from\": \"源概念名\", \"to\": \"目标概念名\", \"kind\": \"Related|Causal|PartOf|Sequence|Dependency|Hierarchical|CoOccurrence\"}}\n\
               ]\n\
             }}\n\n\
             # 示例\n\
             输入: \"0.1.0版本开发,用户认证用JWT,API接口用REST,数据库用PostgreSQL\"\n\
             输出:\n\
             {{\n\
               \"concepts\": [\n\
                 {{\"name\": \"0.1.0版本\", \"type\": \"version\", \"description\": \"当前开发版本\", \"keywords\": [\"0.1.0\", \"版本\"]}},\n\
                 {{\"name\": \"JWT\", \"type\": \"tool\", \"description\": \"用户认证方案\", \"keywords\": [\"JWT\", \"认证\", \"token\"]}},\n\
                 {{\"name\": \"REST API\", \"type\": \"skill\", \"description\": \"API接口设计风格\", \"keywords\": [\"REST\", \"API\", \"接口\"]}},\n\
                 {{\"name\": \"PostgreSQL\", \"type\": \"tool\", \"description\": \"数据库选型\", \"keywords\": [\"PostgreSQL\", \"数据库\"]}}\n\
               ],\n\
               \"relations\": [\n\
                 {{\"from\": \"JWT\", \"to\": \"0.1.0版本\", \"kind\": \"PartOf\"}},\n\
                 {{\"from\": \"REST API\", \"to\": \"0.1.0版本\", \"kind\": \"PartOf\"}},\n\
                 {{\"from\": \"PostgreSQL\", \"to\": \"0.1.0版本\", \"kind\": \"PartOf\"}}\n\
               ]\n\
             }}\n"
        );

        // Distillation responses can be large (many concepts + relations); give
        // the model enough tokens to return a complete, parseable JSON object.
        self.call_api_json(
            SYSTEM_DISTILL,
            &user_prompt,
            4096, // distill
            Some(&Self::params_for_distill()),
            |response| {
                let cleaned = Self::strip_code_blocks(response);
                let result: LlmDistillResult = serde_json::from_str(&cleaned).map_err(|e| {
                    MemHopError::Serialization(format!("Parse distill concepts failed: {}", e))
                })?;
                Ok(result)
            },
        )
        .or_else(|e| {
            tracing::warn!("LLM distill_concepts failed, using fallback: {}", e);
            Ok(self.fallback_distill_concepts(summary))
        })
    }

    fn fallback_distill_concepts(&self, _summary: &str) -> LlmDistillResult {
        LlmDistillResult {
            concepts: vec![],
            relations: vec![],
        }
    }

    fn check_same_topic(&self, summary_a: &str, summary_b: &str) -> Result<bool, MemHopError> {
        let user_prompt = format!(
            "判断以下两段对话摘要是否描述'''同一个话题'''（连续两轮对话是否为同一主题的延续）。\n\n\
             摘要 A: {summary_a}\n\n\
             摘要 B: {summary_b}\n\n\
             只回答 yes 或 no，不要其他内容。"
        );

        self.call_api_json(
            "你是 MemHop 话题一致性判断专家。",
            &user_prompt,
            64,
            Some(&Self::params_for_summarize()),
            |response| {
                let cleaned = response.trim().to_lowercase();
                if cleaned.starts_with("yes") || cleaned.starts_with("y") {
                    Ok(true)
                } else if cleaned.starts_with("no") || cleaned.starts_with("n") {
                    Ok(false)
                } else {
                    Err(MemHopError::Serialization(format!(
                        "Unexpected check_same_topic response: {}",
                        response
                    )))
                }
            },
        )
        .or_else(|e| {
            tracing::warn!("LLM check_same_topic failed, returning false: {}", e);
            Ok(false)
        })
    }

    fn merge_summarize(&self, texts: &[String]) -> Result<(String, String), MemHopError> {
        let numbered = texts
            .iter()
            .enumerate()
            .map(|(i, t)| format!("{}. {}", i + 1, t))
            .collect::<Vec<_>>()
            .join("\n---\n");

        let user_prompt = format!(
            "以下是多轮相邻对话的摘要，它们属于同一个话题，请将它们合并压缩为一个统一的摘要。\n\n\
             {numbered}\n\n\
             # 输出格式(JSON)\n{{\n  \"title\": \"合并后的简短标题(不超过20字)\",\n  \"summary\": \"关键词密集的摘要段落(100-200字)\"\n}}"
        );

        self.call_api_json(
            "你是 MemHop 记忆合并压缩专家。",
            &user_prompt,
            1024,
            Some(&Self::params_for_summarize()),
            |response| {
                let cleaned = Self::strip_code_blocks(response);
                let json: serde_json::Value = serde_json::from_str(&cleaned).map_err(|e| {
                    MemHopError::Serialization(format!("Parse merge_summarize failed: {}", e))
                })?;
                let title = json["title"].as_str().unwrap_or("").to_string();
                let summary = json["summary"].as_str().ok_or_else(|| {
                    MemHopError::Serialization("Missing summary field".to_string())
                })?.to_string();
                Ok((title, summary))
            },
        )
        .or_else(|e| {
            tracing::warn!("LLM merge_summarize failed, using fallback: {}", e);
            let combined = texts.join(" ");
            let title: String = texts.first()
                .and_then(|t| t.chars().next().map(|_c| format!("[Merged] {}", t.chars().take(10).collect::<String>())))
                .unwrap_or_else(|| "[Merged]".to_string());
            let summary = combined.chars().take(200).collect();
            Ok((title, summary))
        })
    }

    fn compress_for_retrieval(&self, text: &str, role: &str) -> Result<String, MemHopError> {
        let user_prompt = format!(
            "# 角色\n{role}\n\n# 内容\n{text}\n\n# 输出格式(JSON)\n{{\n\
               \"keywords\": [\"关键词1\", \"关键词2\"],\n\
               \"summary\": \"压缩摘要(50-200字)\"\n}}\n"
        );

        self.call_api_json(
            "你是 MemHop 记忆提取引擎。请从以下内容中提取关键信息。\n\n\
             要求:\n\
             1. keywords: 提取 3-15 个关键词，保留专有名词、技术术语、版本号、数字\n\
             2. summary: 压缩为关键词密集的摘要(50-200字)，保留所有关键信息\n\
             3. 中英文术语保留原文(如\"JWT\"不翻译)\n\
             4. 用关键词而非口语化表述(如\"用户认证\"而非\"讨论了怎么登录\")\n\n\
             只返回JSON，不要其他内容。",
            &user_prompt,
            256, // retrieval compression: shorter output
            Some(&crate::layers::context::LlmParams {
                temperature: 0.0,
                top_p: 0.8,
                presence_penalty: 0.0,
                frequency_penalty: 0.0,
            }),
            |response| {
                let cleaned = Self::strip_code_blocks(response);
                let json: serde_json::Value = serde_json::from_str(&cleaned).map_err(|e| {
                    MemHopError::Serialization(format!("Parse retrieval compression failed: {}", e))
                })?;
                // Combine keywords + summary for retrieval
                let keywords = json["keywords"]
                    .as_array()
                    .map(|arr| {
                        arr.iter()
                            .filter_map(|v| v.as_str().map(String::from))
                            .collect::<Vec<_>>()
                            .join(" ")
                    })
                    .unwrap_or_default();
                let summary = json["summary"].as_str().unwrap_or("");
                if keywords.is_empty() && summary.is_empty() {
                    return Err(MemHopError::Serialization(
                        "Empty compress_for_retrieval response".to_string(),
                    ));
                }
                let result = if keywords.is_empty() {
                    summary.to_string()
                } else if summary.is_empty() {
                    keywords
                } else {
                    format!("{}\n{}keywords", keywords, summary)
                };
                Ok(result)
            },
        )
        .or_else(|e| {
            tracing::warn!("LLM compress_for_retrieval failed, using fallback: {}", e);
            // Fallback: use keyword extraction via tokenizer
            let tokens = crate::index::sparse::tokenize(text);
            Ok(tokens.join(" "))
        })
    }
}
