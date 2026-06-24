// OpenAI-compatible LLM Provider implementation
//
// Supports any LLM API that follows the OpenAI chat completions format,
// including OpenAI, DeepSeek, and other compatible services.
use crate::config::LlmConfig;
use crate::dream::llm::{
    CrystalDef, CrystalStep, HabitAnalysis, LlmDistillResult, LlmProvider, MemorySummary, Pattern,
};
use crate::MemHopError;
use serde_json::json;
use std::collections::{HashMap, HashSet};

const SYSTEM_SUMMARIZE: &str = "你是 MemHop 记忆压缩专家，擅长从对话场景中提取核心信息。请将输入的记忆片段压缩为结构化摘要。{memory_context}";
const SYSTEM_DISTILL: &str = "你是 MemHop 知识蒸馏引擎，擅长从摘要中提取结构化知识图谱。请分析输入文本，提取核心概念和概念之间的关系。{memory_context}";
const SYSTEM_CRYSTAL: &str = "你是 MemHop 技能结晶系统，擅长从动作模式中提取可复用技能。请分析输入的行为模式，生成结构化的技能定义。{memory_context}";
const SYSTEM_HABITS: &str =
    "你是用户语言习惯分析专家，擅长从对话记录中识别用户的独特语言模式和沟通风格。{memory_context}";
const SYSTEM_PATTERNS: &str =
    "你是 MemHop 行为模式分析专家，擅长从历史记忆中识别重复出现的行为规律。{memory_context}";
const JSON_RETRY_MESSAGE: &str = "请返回纯JSON格式，不要包含markdown代码块标记或任何额外文字。";

/// OpenAI-compatible LLM provider
///
/// Works with any API that follows the OpenAI chat completions format.
/// Configure with your preferred provider's API key, endpoint URL, and model name.
#[allow(dead_code)]
pub struct OpenAICompatibleLlmProvider {
    /// LLM configuration (model, endpoint, temperature, timeout, ...)
    config: LlmConfig,
    /// HTTP client for API calls
    client: reqwest::blocking::Client,
}

impl OpenAICompatibleLlmProvider {
    /// Create a new LLM provider from configuration
    ///
    /// # Arguments
    /// * `config` - `LlmConfig` containing model, api_base, api_key, temperature, timeout
    pub fn new(config: LlmConfig) -> Self {
        Self {
            config,
            client: reqwest::blocking::Client::new(),
        }
    }

    /// Return the full OpenAI-compatible chat completions URL
    fn api_url(&self) -> String {
        self.config.api_url()
    }

    /// Return an optional memory context snippet to inject into prompts.
    ///
    /// Currently returns an empty string; this placeholder can later be wired
    /// to L0 profile fragments.
    fn memory_context(&self) -> String {
        String::new()
    }

    /// Call the OpenAI-compatible API with a complete message list
    fn call_api_messages(
        &self,
        messages: &[serde_json::Value],
        max_tokens: u32,
    ) -> Result<String, MemHopError> {
        let body = json!({
            "model": self.config.model,
            "messages": messages,
            "max_tokens": max_tokens,
            "temperature": self.config.temperature,
        });

        let response = self
            .client
            .post(self.api_url())
            .bearer_auth(&self.config.api_key)
            .json(&body)
            .timeout(std::time::Duration::from_secs(self.config.timeout_secs))
            .send()
            .map_err(|e| MemHopError::Serialization(format!("API call failed: {}", e)))?;

        if !response.status().is_success() {
            return Err(MemHopError::Serialization(format!(
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
        parse: F,
    ) -> Result<T, MemHopError>
    where
        F: Fn(&str) -> Result<T, MemHopError>,
    {
        let memory_context = self.memory_context();
        let system = system.replace("{memory_context}", &memory_context);
        let user_prompt = user_prompt.replace("{memory_context}", &memory_context);

        let mut messages = vec![
            json!({"role": "system", "content": system}),
            json!({"role": "user", "content": user_prompt}),
        ];

        let response = self.call_api_messages(&messages, max_tokens)?;
        if let Ok(value) = parse(&response) {
            return Ok(value);
        }

        // Retry once with a format reminder
        messages.push(json!({"role": "user", "content": JSON_RETRY_MESSAGE}));
        let response = self.call_api_messages(&messages, max_tokens)?;
        parse(&response)
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
    fn summarize(&self, texts: &[String]) -> Result<String, MemHopError> {
        let memories_text = texts
            .iter()
            .enumerate()
            .map(|(i, t)| format!("{}. {}", i + 1, t))
            .collect::<Vec<_>>()
            .join("\n");

        let user_prompt = format!(
            "# 任务\n\
             请分析以下记忆片段，提取:\n\
             1. 核心主题(1-2个关键词)\n\
             2. 关键信息点(不超过3条)\n\
             3. 一句话总结(不超过50字)\n\n\
             # 输出格式\n\
             请以JSON格式返回:\n\
             {{\n\
               \"theme\": \"核心主题\",\n\
               \"key_points\": [\"关键点1\", \"关键点2\"],\n\
               \"summary\": \"一句话总结\"\n\
             }}\n\n\
             # 输入数据\n\
             {memories_text}\n\n\
             # 开始分析\n"
        );

        self.call_api_json(
            SYSTEM_SUMMARIZE,
            &user_prompt,
            512, // summarize
            |response| {
                let cleaned = Self::strip_code_blocks(response);
                let json: serde_json::Value = serde_json::from_str(&cleaned).map_err(|e| {
                    MemHopError::Serialization(format!("Parse summary failed: {}", e))
                })?;
                let summary = json["summary"]
                    .as_str()
                    .map(|s| s.to_string())
                    .ok_or_else(|| {
                        MemHopError::Serialization("Missing summary field".to_string())
                    })?;
                Ok(summary)
            },
        )
        .or_else(|_| Ok(self.fallback_summarize(texts)))
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
        .or_else(|_| Ok(self.fallback_extract_patterns(memories)))
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
        .or_else(|_| Ok(self.fallback_generate_crystal(pattern)))
    }

    fn fallback_summarize(&self, texts: &[String]) -> String {
        // 提取所有文本的关键词并拼接
        let mut keyword_freq: HashMap<String, usize> = HashMap::new();
        for text in texts {
            for word in text.split_whitespace() {
                if word.len() > 2 {
                    // 过滤短词
                    *keyword_freq.entry(word.to_lowercase()).or_insert(0) += 1;
                }
            }
        }

        let mut sorted: Vec<_> = keyword_freq.into_iter().collect();
        sorted.sort_by_key(|b| std::cmp::Reverse(b.1));

        sorted
            .into_iter()
            .take(10)
            .map(|(k, _)| k)
            .collect::<Vec<_>>()
            .join(", ")
    }

    fn fallback_extract_patterns(&self, memories: &[MemorySummary]) -> Vec<Pattern> {
        // 计算 keywords 交集
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
        // 正则提取 "when/if → then" 模式
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
                confidence: pattern.confidence * 0.8, // 降低置信度
            }
        } else {
            // 无法提取，返回通用模板
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
        .or_else(|_| Ok(self.fallback_analyze_user_habits(dialogues)))
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

        // Take top 10 most frequent words as lexicon entries
        let mut sorted: Vec<_> = word_freq.into_iter().collect();
        sorted.sort_by_key(|b| std::cmp::Reverse(b.1));

        let mut lexicon = HashMap::new();
        for (word, freq) in sorted.into_iter().take(10) {
            lexicon.insert(word.clone(), format!("高频词(出现{}次)", freq));
        }

        HabitAnalysis {
            lexicon,
            style_traits: Vec::new(), // Cannot determine style without LLM
            emotion_patterns: HashMap::new(), // Cannot determine emotions without LLM
        }
    }

    fn distill_concepts(&self, summary: &str) -> Result<LlmDistillResult, MemHopError> {
        let user_prompt = format!(
            "请分析以下摘要，提取核心概念和概念之间的关系。\n\n\
             返回严格的JSON格式，包含两个字段:\n\
             - concepts: 概念数组，每个概念包含 name、type、description、keywords\n\
             - relations: 关系数组，每个关系包含 from、to、kind\n\n\
             kind 可选值: Related、Causal、PartOf、Sequence、Dependency。\n\n\
             摘要:\n{summary}\n"
        );

        // Distillation responses can be large (many concepts + relations); give
        // the model enough tokens to return a complete, parseable JSON object.
        self.call_api_json(
            SYSTEM_DISTILL,
            &user_prompt,
            4096, // distill
            |response| {
                let cleaned = Self::strip_code_blocks(response);
                let result: LlmDistillResult = serde_json::from_str(&cleaned).map_err(|e| {
                    MemHopError::Serialization(format!("Parse distill concepts failed: {}", e))
                })?;
                Ok(result)
            },
        )
        .or_else(|_| Ok(self.fallback_distill_concepts(summary)))
    }

    fn fallback_distill_concepts(&self, _summary: &str) -> LlmDistillResult {
        LlmDistillResult {
            concepts: vec![],
            relations: vec![],
        }
    }
}
