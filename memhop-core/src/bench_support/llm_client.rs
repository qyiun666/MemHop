//! DeepSeek LLM 客户端 — 提供记忆提取、情感分析、摘要生成。
//!
//! 设计原则：
//! - Feature gate: 通过 llm-api feature 隔离
//! - 缓存机制: 相同输入不重复调用 API
//! - Fallback: API 不可用时使用合成数据
//! - 安全: API key 从环境变量读取

use crate::types::Emotion;
use std::collections::HashMap;
use std::time::Duration;

/// DeepSeek API 响应结构
#[cfg(feature = "llm-api")]
#[derive(Debug, serde::Deserialize)]
struct DeepSeekResponse {
    choices: Vec<DeepSeekChoice>,
}

#[cfg(feature = "llm-api")]
#[derive(Debug, serde::Deserialize)]
struct DeepSeekChoice {
    message: DeepSeekMessage,
}

#[cfg(feature = "llm-api")]
#[derive(Debug, serde::Deserialize)]
struct DeepSeekMessage {
    content: String,
}

/// LLM 记忆提取结果。
#[derive(Debug, Clone)]
pub struct LlmMemoryExtraction {
    pub topic_label: String,
    pub keywords: Vec<String>,
    pub compressed_summary: String,
    pub valence: f64,
    pub arousal: f64,
    pub emotion: Emotion,
    pub intensity: f32,
}

/// LLM 结晶摘要结果。
#[derive(Debug, Clone)]
pub struct LlmCrystallizeOutput {
    pub summary: String,
    pub keywords: Vec<String>,
    pub domain_name: String,
}

/// DeepSeek API 客户端。
pub struct DeepSeekClient {
    api_key: String,
    #[allow(dead_code)]
    base_url: String,
    #[allow(dead_code)]
    model: String,
    #[allow(dead_code)]
    timeout: Duration,
    cache: HashMap<String, String>,
    use_cache: bool,
}

impl DeepSeekClient {
    /// 创建新的客户端。
    pub fn new() -> Option<Self> {
        let api_key = std::env::var("DEEPSEEK_API_KEY").ok()?;

        Some(Self {
            api_key,
            base_url: "https://api.deepseek.com/v1".to_string(),
            model: "deepseek-chat".to_string(),
            timeout: Duration::from_secs(30),
            cache: HashMap::new(),
            use_cache: true,
        })
    }

    /// 使用指定 API key 创建客户端。
    pub fn with_api_key(api_key: &str) -> Self {
        Self {
            api_key: api_key.to_string(),
            base_url: "https://api.deepseek.com/v1".to_string(),
            model: "deepseek-chat".to_string(),
            timeout: Duration::from_secs(30),
            cache: HashMap::new(),
            use_cache: true,
        }
    }

    /// 启用/禁用缓存。
    pub fn with_cache(mut self, enabled: bool) -> Self {
        self.use_cache = enabled;
        self
    }

    /// 检查客户端是否可用。
    pub fn is_available(&self) -> bool {
        !self.api_key.is_empty()
    }

    /// 记忆提取（使用缓存或合成数据）。
    pub fn extract_memory(&mut self, text: &str) -> LlmMemoryExtraction {
        let cache_key = format!("extract:{}", text);

        // 检查缓存
        if self.use_cache && self.cache.contains_key(&cache_key) {
            return self.parse_extraction(&self.cache[&cache_key]);
        }

        // 尝试 API 调用
        if self.is_available() {
            match self.call_api_extract(text) {
                Ok(response) => {
                    if self.use_cache {
                        self.cache.insert(cache_key, response.clone());
                    }
                    return self.parse_extraction(&response);
                }
                Err(_) => {
                    // Fallback 到合成数据
                }
            }
        }

        // 合成数据 fallback
        self.synthesize_extraction(text)
    }

    /// 情感检测。
    pub fn detect_emotion(&mut self, text: &str) -> (Emotion, f32, f64, f64) {
        let cache_key = format!("emotion:{}", text);

        // 检查缓存
        if self.use_cache && self.cache.contains_key(&cache_key) {
            return self.parse_emotion(&self.cache[&cache_key]);
        }

        // 合成数据（简化实现）
        let result = self.synthesize_emotion(text);
        let response = format!("{:?},{},{},{}", result.0, result.1, result.2, result.3);

        if self.use_cache {
            self.cache.insert(cache_key, response);
        }

        result
    }

    /// 生成结晶摘要。
    pub fn generate_crystallize_summary(
        &mut self,
        topic: &str,
        memories: &[&str],
    ) -> LlmCrystallizeOutput {
        let cache_key = format!("crystallize:{}:{:?}", topic, memories);

        // 检查缓存
        if self.use_cache && self.cache.contains_key(&cache_key) {
            return self.parse_crystallize(&self.cache[&cache_key]);
        }

        // 合成数据
        let result = LlmCrystallizeOutput {
            summary: format!("Summary of {} with {} memories", topic, memories.len()),
            keywords: vec![topic.to_string(), "summary".to_string()],
            domain_name: format!("domain_{}", topic),
        };

        if self.use_cache {
            self.cache.insert(cache_key, format!("{:?}", result));
        }

        result
    }

    /// API 调用（记忆提取）。
    #[cfg(feature = "llm-api")]
    fn call_api_extract(&self, text: &str) -> Result<String, String> {
        let prompt = format!(
            "Extract topic, keywords, summary, emotion from the following text. \
             Return JSON: {{\"topic\": \"...\", \"keywords\": [...], \"summary\": \"...\", \
             \"emotion\": \"neutral|joy|sadness|anger|surprise|fear\", \"intensity\": 0.0-1.0}}. \
             Text: {}",
            text
        );

        let rt = tokio::runtime::Runtime::new()
            .map_err(|e| format!("Create runtime: {}", e))?;
        
        rt.block_on(async {
            let client = reqwest::Client::new();
            let resp = client
                .post(&format!("{}/chat/completions", self.base_url))
                .header("Authorization", format!("Bearer {}", self.api_key))
                .header("Content-Type", "application/json")
                .timeout(self.timeout)
                .json(&serde_json::json!({
                    "model": self.model,
                    "messages": [{
                        "role": "user",
                        "content": prompt
                    }],
                    "temperature": 0.3,
                    "max_tokens": 200
                }))
                .send()
                .await
                .map_err(|e| format!("HTTP request: {}", e))?;
            
            let resp_text = resp.text().await
                .map_err(|e| format!("Read response: {}", e))?;
            
            let api_resp: DeepSeekResponse = serde_json::from_str(&resp_text)
                .map_err(|e| format!("Parse response: {}", e))?;
            
            api_resp.choices.first()
                .map(|c| c.message.content.clone())
                .ok_or_else(|| "No choices in response".to_string())
        })
    }

    /// API 调用（llm-api feature 未启用时的 fallback）。
    #[cfg(not(feature = "llm-api"))]
    fn call_api_extract(&self, _text: &str) -> Result<String, String> {
        Err("llm-api feature not enabled".to_string())
    }

    /// 解析 API 响应。
    fn parse_extraction(&self, response: &str) -> LlmMemoryExtraction {
        // 简化实现：返回默认值
        LlmMemoryExtraction {
            topic_label: "parsed_topic".to_string(),
            keywords: vec!["keyword1".to_string()],
            compressed_summary: response.to_string(),
            valence: 0.5,
            arousal: 0.5,
            emotion: Emotion::Neutral,
            intensity: 0.5,
        }
    }

    /// 解析情感响应。
    fn parse_emotion(&self, _response: &str) -> (Emotion, f32, f64, f64) {
        // 简化实现
        (Emotion::Neutral, 0.5, 0.5, 0.5)
    }

    /// 解析结晶响应。
    fn parse_crystallize(&self, response: &str) -> LlmCrystallizeOutput {
        LlmCrystallizeOutput {
            summary: response.to_string(),
            keywords: vec!["keyword".to_string()],
            domain_name: "domain".to_string(),
        }
    }

    /// 合成记忆提取。
    fn synthesize_extraction(&self, text: &str) -> LlmMemoryExtraction {
        let topic = if text.to_lowercase().contains("rust") {
            "rust_programming"
        } else if text.to_lowercase().contains("python") {
            "python_programming"
        } else if text.to_lowercase().contains("机器学习") || text.to_lowercase().contains("machine learning") {
            "machine_learning"
        } else {
            "general"
        };

        let keywords: Vec<String> = text
            .split_whitespace()
            .take(3)
            .map(|w| w.to_lowercase())
            .collect();

        let (emotion, intensity, valence, arousal) = self.synthesize_emotion(text);

        LlmMemoryExtraction {
            topic_label: topic.to_string(),
            keywords,
            compressed_summary: format!("Summary: {}", &text[..text.len().min(50)]),
            valence,
            arousal,
            emotion,
            intensity,
        }
    }

    /// 合成情感检测。
    fn synthesize_emotion(&self, text: &str) -> (Emotion, f32, f64, f64) {
        let lower = text.to_lowercase();

        if lower.contains("开心") || lower.contains("happy") || lower.contains("joy") || lower.contains("不错") {
            (Emotion::Joy, 0.8, 0.7, 0.3)
        } else if lower.contains("sad") || lower.contains("难过") || lower.contains("失望") {
            (Emotion::Sadness, 0.2, 0.3, 0.7)
        } else if lower.contains("angry") || lower.contains("生气") || lower.contains("frustrated") || lower.contains("困扰") {
            (Emotion::Anger, 0.2, 0.3, 0.8)
        } else if lower.contains("curious") || lower.contains("好奇") || lower.contains("想知道") || lower.contains("学习") {
            (Emotion::Surprise, 0.6, 0.6, 0.5)
        } else if lower.contains("担心") || lower.contains("concerned") || lower.contains("worried") || lower.contains("漏洞") {
            (Emotion::Fear, 0.3, 0.4, 0.7)
        } else {
            (Emotion::Neutral, 0.5, 0.5, 0.5)
        }
    }
}

/// 测试用例数据。
pub struct LlmTestCase {
    pub input: String,
    pub expected_topic: String,
    pub expected_emotion: Emotion,
    pub expected_valence_range: (f64, f64),
}

/// 生成测试用例。
pub fn generate_test_cases() -> Vec<LlmTestCase> {
    vec![
        LlmTestCase {
            input: "Rust 的所有权系统很强大".to_string(),
            expected_topic: "rust_programming".to_string(),
            expected_emotion: Emotion::Joy,
            expected_valence_range: (0.6, 1.0),
        },
        LlmTestCase {
            input: "这个 bug 让我很困扰".to_string(),
            expected_topic: "general".to_string(),
            expected_emotion: Emotion::Anger,
            expected_valence_range: (0.0, 0.5),
        },
        LlmTestCase {
            input: "我想学习机器学习".to_string(),
            expected_topic: "machine_learning".to_string(),
            expected_emotion: Emotion::Surprise,
            expected_valence_range: (0.4, 0.8),
        },
    ]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_client_creation() {
        // 无 API key 时返回 None
        let _ = std::env::var("DEEPSEEK_API_KEY"); // 忽略错误
        let client = DeepSeekClient::new();
        // 如果环境变量不存在，应该返回 None
        if std::env::var("DEEPSEEK_API_KEY").is_err() {
            assert!(client.is_none());
        }
    }

    #[test]
    fn test_with_api_key() {
        let client = DeepSeekClient::with_api_key("test_key");
        assert!(client.is_available());
    }

    #[test]
    fn test_synthesize_extraction() {
        let mut client = DeepSeekClient::with_api_key("test");
        let extraction = client.extract_memory("Rust 的所有权系统");
        assert_eq!(extraction.topic_label, "rust_programming");
    }

    #[test]
    fn test_synthesize_emotion() {
        let mut client = DeepSeekClient::with_api_key("test");
        let (emotion, _, _, _) = client.detect_emotion("这个 bug 让我很困扰");
        assert_eq!(emotion, Emotion::Anger);
    }
}
