//! 基准测试支撑模块 — 提供内存监控、指标计算等工具。

pub mod metrics;
pub mod memory_monitor;
pub mod report;
pub mod test_data;

// v0.24.0: 端到端 Agent 集成测试支撑
pub mod agent_simulator;
pub mod dataset_loader;
pub mod llm_client;
