//! MemHop 公共基准测试基础设施模块
//!
//! 提供基准测试所需的各种工具和组件：
//! - `metrics`: IR 指标计算（Recall@K, MRR, NDCG, Precision@K, 延迟统计）
//! - `data_gen`: 合成数据生成器，用于规模扩展性测试
//! - `llm_judge`: DeepSeek LLM-as-Judge 语义评分客户端
//! - `report`: 基准测试报告生成器
//! - `mock_meowvec`: 自动启动/停止 mock gRPC 编码服务

#![allow(dead_code, unused_imports)]

pub mod data_gen;
pub mod llm_judge;
pub mod metrics;
pub mod mock_meowvec;
pub mod report;

// Re-export 主要类型和函数，方便使用
pub use metrics::{latency_stats, mrr, ndcg_at_k, precision_at_k, recall_at_k, LatencyStats};

pub use data_gen::{generate_dataset, GeneratedItem, GeneratedQuestion};

pub use llm_judge::LlmJudge;

pub use report::{generate_json_report, generate_markdown_report, BenchResult, CompetitorBaseline};

pub use mock_meowvec::{kill_python_meowvec, spawn_python_meowvec};
