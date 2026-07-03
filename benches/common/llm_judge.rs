//! DeepSeek LLM-as-Judge 客户端模块
//!
//! 提供基于 DeepSeek API 的 LLM 评判功能：
//! - 基于检索上下文生成答案
//! - LLM-as-Judge 语义评分（0.0-1.0）
//! - 指数退避重试机制

#![allow(dead_code, unused_imports)]

use serde::{Deserialize, Serialize};
use std::time::Duration;

/// API 配置常量
const API_URL: &str = "https://api.deepseek.com/v1/chat/completions";
const MODEL: &str = "deepseek-chat";
const MAX_RETRIES: u32 = 3;
const INITIAL_RETRY_DELAY: Duration = Duration::from_secs(1);

/// DeepSeek API 请求
#[derive(Debug, Serialize)]
struct ChatRequest {
    model: String,
    messages: Vec<ChatMessage>,
    temperature: f64,
    max_tokens: u32,
}

/// 聊天消息
#[derive(Debug, Serialize, Deserialize)]
struct ChatMessage {
    role: String,
    content: String,
}

/// DeepSeek API 响应
#[derive(Debug, Deserialize)]
struct ChatResponse {
    choices: Vec<Choice>,
}

/// 响应选项
#[derive(Debug, Deserialize)]
struct Choice {
    message: ChatMessage,
}

/// LLM-as-Judge 客户端
pub struct LlmJudge {
    client: reqwest::blocking::Client,
    api_key: String,
}

impl LlmJudge {
    /// 创建新的 LlmJudge 实例
    ///
    /// # 返回
    /// - `Some(Self)`: 如果环境变量 MEMHOP_DEEPSEEK_API_KEY 存在
    /// - `None`: 如果环境变量不存在
    pub fn new() -> Option<Self> {
        let api_key = std::env::var("MEMHOP_DEEPSEEK_API_KEY").ok()?;

        let client = reqwest::blocking::Client::builder()
            .timeout(Duration::from_secs(60))
            .build()
            .ok()?;

        Some(Self { client, api_key })
    }

    /// 发送 API 请求（带重试）
    fn send_request(
        &self,
        request: &ChatRequest,
    ) -> Result<ChatResponse, Box<dyn std::error::Error>> {
        let mut last_error = None;

        for attempt in 0..MAX_RETRIES {
            if attempt > 0 {
                let delay = INITIAL_RETRY_DELAY * 2u32.pow(attempt - 1);
                std::thread::sleep(delay);
            }

            match self
                .client
                .post(API_URL)
                .header("Authorization", format!("Bearer {}", self.api_key))
                .header("Content-Type", "application/json")
                .json(request)
                .send()
            {
                Ok(response) => {
                    if response.status().is_success() {
                        let chat_response: ChatResponse = response.json()?;
                        return Ok(chat_response);
                    } else {
                        let status = response.status();
                        let body = response.text().unwrap_or_default();
                        last_error = Some(format!("HTTP {}: {}", status, body));
                    }
                }
                Err(e) => {
                    last_error = Some(e.to_string());
                }
            }
        }

        Err(format!("Max retries exceeded. Last error: {:?}", last_error).into())
    }

    /// 基于检索上下文生成答案
    ///
    /// # 参数
    /// - `context`: 检索到的上下文
    /// - `question`: 用户问题
    ///
    /// # 返回
    /// 生成的答案文本
    pub fn generate_answer(
        &self,
        context: &str,
        question: &str,
    ) -> Result<String, Box<dyn std::error::Error>> {
        let prompt = format!(
            "基于以下上下文回答问题。如果上下文中没有相关信息，请说明。\n\n\
             上下文：\n{}\n\n\
             问题：{}\n\n\
             答案：",
            context, question
        );

        let request = ChatRequest {
            model: MODEL.to_string(),
            messages: vec![
                ChatMessage {
                    role: "system".to_string(),
                    content: "你是一个有帮助的助手，基于提供的上下文回答问题。".to_string(),
                },
                ChatMessage {
                    role: "user".to_string(),
                    content: prompt,
                },
            ],
            temperature: 0.3,
            max_tokens: 1024,
        };

        let response = self.send_request(&request)?;
        let answer = response
            .choices
            .first()
            .map(|c| c.message.content.clone())
            .unwrap_or_default();

        Ok(answer)
    }

    /// LLM-as-Judge 语义评分
    ///
    /// 评估生成答案与标准答案的语义相似度
    ///
    /// # 参数
    /// - `generated`: 生成的答案
    /// - `ground_truth`: 标准答案
    /// - `question`: 原始问题
    ///
    /// # 返回
    /// 0.0 到 1.0 之间的评分
    pub fn judge_answer(
        &self,
        generated: &str,
        ground_truth: &str,
        question: &str,
    ) -> Result<f64, Box<dyn std::error::Error>> {
        let prompt = format!(
            "你是一个严格的答案质量评估专家。请评估以下生成的答案相对于标准答案的质量。\n\n\
             问题：{}\n\n\
             标准答案：{}\n\n\
             生成的答案：{}\n\n\
             请从以下维度评估（0.0-1.0分）：\n\
             1. 事实准确性（答案中的事实是否正确）\n\
             2. 完整性（是否覆盖了标准答案的关键点）\n\
             3. 相关性（是否直接回答了问题）\n\n\
             请只返回一个数字评分（0.0-1.0），不要包含其他内容。",
            question, ground_truth, generated
        );

        let request = ChatRequest {
            model: MODEL.to_string(),
            messages: vec![
                ChatMessage {
                    role: "system".to_string(),
                    content: "你是一个答案质量评估专家，只返回数字评分。".to_string(),
                },
                ChatMessage {
                    role: "user".to_string(),
                    content: prompt,
                },
            ],
            temperature: 0.1,
            max_tokens: 10,
        };

        let response = self.send_request(&request)?;
        let score_text = response
            .choices
            .first()
            .map(|c| c.message.content.trim().to_string())
            .unwrap_or_else(|| "0.0".to_string());

        // 解析评分
        let score: f64 = score_text
            .parse()
            .map_err(|_| format!("Failed to parse score: '{}'", score_text))?;

        // 确保评分在有效范围内
        Ok(score.clamp(0.0, 1.0))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_llm_judge_new_without_env() {
        // 确保在没有环境变量时返回 None
        // 注意：这个测试可能需要在干净的环境中运行
        let judge = LlmJudge::new();
        // 如果环境变量不存在，应该返回 None
        if std::env::var("MEMHOP_DEEPSEEK_API_KEY").is_err() {
            assert!(judge.is_none());
        }
    }

    // 以下测试需要真实的 API key，标记为 ignore
    #[test]
    #[ignore]
    fn test_generate_answer() {
        let judge = LlmJudge::new().expect("MEMHOP_DEEPSEEK_API_KEY not set");
        let result = judge.generate_answer(
            "Rust 是一门系统编程语言，注重安全性和性能。",
            "Rust 是什么？",
        );
        assert!(result.is_ok());
        let answer = result.unwrap();
        assert!(!answer.is_empty());
    }

    #[test]
    #[ignore]
    fn test_judge_answer() {
        let judge = LlmJudge::new().expect("MEMHOP_DEEPSEEK_API_KEY not set");
        let result = judge.judge_answer(
            "Rust 是一门系统编程语言",
            "Rust 是一门注重安全性的系统编程语言",
            "Rust 是什么？",
        );
        assert!(result.is_ok());
        let score = result.unwrap();
        assert!(score >= 0.0 && score <= 1.0);
    }
}
