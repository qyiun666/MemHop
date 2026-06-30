//! MemHop 公共基准测试基础设施模块
//!
//! 提供基准测试所需的各种工具和组件：
//! - `metrics`: IR 指标计算（Recall@K, MRR, NDCG, Precision@K, 延迟统计）
//! - `data_gen`: 合成数据生成器，用于规模扩展性测试
//! - `llm_judge`: DeepSeek LLM-as-Judge 语义评分客户端
//! - `report`: 基准测试报告生成器

pub mod metrics;
pub mod data_gen;
pub mod llm_judge;
pub mod report;

// Re-export 主要类型和函数，方便使用
pub use metrics::{
    recall_at_k,
    mrr,
    ndcg_at_k,
    precision_at_k,
    latency_stats,
    LatencyStats,
};

pub use data_gen::{
    generate_dataset,
    GeneratedItem,
    GeneratedQuestion,
};

pub use llm_judge::LlmJudge;

pub use report::{
    generate_json_report,
    generate_markdown_report,
    BenchResult,
    CompetitorBaseline,
};
