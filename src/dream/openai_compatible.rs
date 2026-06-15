// OpenAI-compatible LLM Provider implementation
//
// Supports any LLM API that follows the OpenAI chat completions format,
// including OpenAI, DeepSeek, and other compatible services.
use crate::dream::llm::{CrystalDef, LlmProvider, MemorySummary, Pattern};
use crate::MemHopError;
use serde_json::json;
use std::collections::{HashMap, HashSet};

/// OpenAI-compatible LLM provider
///
/// Works with any API that follows the OpenAI chat completions format.
/// Configure with your preferred provider's API key, endpoint URL, and model name.
#[allow(dead_code)]
pub struct OpenAICompatibleLlmProvider {
    /// API key for authentication
    api_key: String,
    /// API endpoint URL (must be OpenAI-compatible)
    api_url: String,
    /// Model name to use
    model: String,
    /// HTTP client for API calls
    client: reqwest::blocking::Client,
}

impl OpenAICompatibleLlmProvider {
    /// Create a new LLM provider with custom configuration
    ///
    /// # Arguments
    /// * `api_key` - API key for authentication
    /// * `api_url` - API endpoint URL (e.g., "https://api.openai.com/v1/chat/completions")
    /// * `model` - Model name (e.g., "gpt-4", "deepseek-chat")
    pub fn new(api_key: String, api_url: String, model: String) -> Self {
        Self {
            api_key,
            api_url,
            model,
            client: reqwest::blocking::Client::new(),
        }
    }

    /// Call the OpenAI-compatible API with a prompt
    ///
    /// # Arguments
    /// * `prompt` - The prompt to send to the API
    /// * `max_tokens` - Maximum tokens in response
    ///
    /// # Returns
    /// Response text from the API
    fn call_api(&self, prompt: &str, max_tokens: u32) -> Result<String, MemHopError> {
        let body = json!({
            "model": self.model,
            "messages": [
                {"role": "system", "content": "You are a memory consolidation assistant."},
                {"role": "user", "content": prompt}
            ],
            "max_tokens": max_tokens,
            "temperature": 0.3,
        });

        let response = self.client
            .post(&self.api_url)
            .bearer_auth(&self.api_key)
            .json(&body)
            .timeout(std::time::Duration::from_secs(30))
            .send()
            .map_err(|e| MemHopError::Serialization(format!("API call failed: {}", e)))?;

        if !response.status().is_success() {
            return Err(MemHopError::Serialization(
                format!("API request failed: {} - {}", response.status(), response.text().unwrap_or_default())
            ));
        }

        let json: serde_json::Value = response.json()
            .map_err(|e| MemHopError::Serialization(format!("Parse response failed: {}", e)))?;

        json["choices"][0]["message"]["content"]
            .as_str()
            .map(|s| s.to_string())
            .ok_or_else(|| MemHopError::Serialization("No content in response".to_string()))
    }
}

impl LlmProvider for OpenAICompatibleLlmProvider {
    fn summarize(&self, texts: &[String]) -> Result<String, MemHopError> {
        let memories_text = texts.iter().enumerate()
            .map(|(i, t)| format!("{}. {}", i + 1, t))
            .collect::<Vec<_>>()
            .join("\n");

        let prompt = format!(
            "# 角色\n\
             你是记忆整理专家,擅长从碎片化信息中提取核心主题。\n\n\
             # 任务\n\
             请分析以下记忆片段,提取:\n\
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

        let response = self.call_api(&prompt, 512)?;

        // 解析JSON响应并提取summary字段
        let json: serde_json::Value = serde_json::from_str(&response)
            .map_err(|e| MemHopError::Serialization(format!("Parse summary failed: {}", e)))?;

        Ok(json["summary"].as_str()
            .unwrap_or(&response)  // 如果解析失败,返回原始响应
            .to_string())
    }

    fn extract_patterns(&self, memories: &[MemorySummary]) -> Result<Vec<Pattern>, MemHopError> {
        let memories_text = memories.iter().enumerate()
            .map(|(i, m)| format!("{}. [{}] {}\n   Keywords: {}", 
                i + 1, 
                chrono::DateTime::from_timestamp_millis(m.timestamp)
                    .map(|dt| dt.format("%Y-%m-%d").to_string())
                    .unwrap_or_else(|| "unknown".to_string()),
                m.text,
                m.keywords.join(", ")
            ))
            .collect::<Vec<_>>()
            .join("\n");

        let prompt = format!(
            "# 角色\n\
             你是行为模式分析专家,擅长从历史记忆中识别重复出现的行为规律。\n\n\
             # 任务\n\
             分析以下记忆条目,提取重复出现的行为模式。重点关注:\n\
             - 高频出现的动作或决策\n\
             - 相似场景下的反应模式\n\
             - 时间或情境相关的规律\n\n\
             # 输出格式\n\
             返回JSON数组,每个对象包含:\n\
             - description: string (模式描述,清晰简洁)\n\
             - frequency: integer (出现频率,1-100)\n\
             - confidence: float (置信度,0.0-1.0)\n\n\
             # 示例\n\
             [\n\
               {{\"description\": \"每周三晚上学习新技术\", \"frequency\": 85, \"confidence\": 0.9}},\n\
               {{\"description\": \"遇到问题时先查阅文档再求助\", \"frequency\": 70, \"confidence\": 0.8}}\n\
             ]\n\n\
             # 输入数据\n\
             {memories_text}\n\n\
             # 开始分析\n"
        );

        let response = self.call_api(&prompt, 1024)?;

        // 解析 JSON 响应
        let patterns: Vec<serde_json::Value> = serde_json::from_str(&response)
            .map_err(|e| MemHopError::Serialization(format!("Parse patterns failed: {}", e)))?;

        Ok(patterns.into_iter().map(|p| Pattern {
            description: p["description"].as_str().unwrap_or("").to_string(),
            frequency: p["frequency"].as_u64().unwrap_or(1) as u32,
            confidence: p["confidence"].as_f64().unwrap_or(0.5) as f32,
        }).collect())
    }

    fn generate_crystal(&self, pattern: &Pattern) -> Result<CrystalDef, MemHopError> {
        let prompt = format!(
            "# 角色\n\
             你是规则引擎专家,擅长将行为模式转化为可执行的结晶规则。\n\n\
             # 任务\n\
             基于以下行为模式,生成一个可执行的结晶规则(DSL格式)。\n\
             规则应包含:\n\
             1. condition: 触发条件(使用DSL语法,如 \"time.weekday == 3 AND time.hour >= 20\")\n\
             2. action: 执行动作(简洁明了的操作指令)\n\
             3. confidence: 规则的置信度(0.0-1.0)\n\n\
             # DSL语法参考\n\
             - 时间条件: time.weekday (0-6), time.hour (0-23), time.minute\n\
             - 比较操作: ==, !=, >, <, >=, <=\n\
             - 逻辑操作: AND, OR, NOT\n\
             - 示例: \"time.weekday == 3 AND time.hour >= 20 AND context.location == 'home'\"\n\n\
             # 输出格式\n\
             返回JSON对象:\n\
             {{\n\
               \"condition\": \"DSL格式条件\",\n\
               \"action\": \"执行动作\",\n\
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

        let response = self.call_api(&prompt, 512)?;

        let json: serde_json::Value = serde_json::from_str(&response)
            .map_err(|e| MemHopError::Serialization(format!("Parse crystal failed: {}", e)))?;

        Ok(CrystalDef {
            condition: json["condition"].as_str().unwrap_or("").to_string(),
            action: json["action"].as_str().unwrap_or("").to_string(),
            confidence: json["confidence"].as_f64().unwrap_or(pattern.confidence as f64) as f32,
        })
    }

    fn fallback_summarize(&self, texts: &[String]) -> String {
        // 提取所有文本的关键词并拼接
        let mut keyword_freq: HashMap<String, usize> = HashMap::new();
        for text in texts {
            for word in text.split_whitespace() {
                if word.len() > 2 {  // 过滤短词
                    *keyword_freq.entry(word.to_lowercase()).or_insert(0) += 1;
                }
            }
        }
        
        let mut sorted: Vec<_> = keyword_freq.into_iter().collect();
        sorted.sort_by_key(|b| std::cmp::Reverse(b.1));
        
        sorted.into_iter()
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
        let common_keywords: Vec<_> = keyword_count.into_iter()
            .filter(|(_, count)| *count >= 2)  // 至少出现在2个记忆中
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
            CrystalDef {
                condition: caps[1].trim().to_string(),
                action: caps[2].trim().to_string(),
                confidence: pattern.confidence * 0.8,  // 降低置信度
            }
        } else {
            // 无法提取,返回通用模板
            CrystalDef {
                condition: format!("trigger: {}", pattern.description.chars().take(50).collect::<String>()),
                action: "log_and_notify".to_string(),
                confidence: 0.3,
            }
        }
    }
}
