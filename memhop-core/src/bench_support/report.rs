//! 基准测试报告生成器 — 输出结构化 JSON 和 Markdown 格式的报告。

use serde::{Deserialize, Serialize};

/// 环境信息。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EnvironmentInfo {
    pub os: String,
    pub arch: String,
    pub rust_version: String,
    pub memhop_version: String,
    pub encoder: String,
    pub timestamp: String,
}

/// 检索性能指标。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RetrievalMetrics {
    pub dataset: String,
    pub ndcg_at_10: f64,
    pub recall_at_5: f64,
    pub recall_at_10: f64,
    pub recall_at_20: f64,
    pub precision_at_10: f64,
    pub mrr: f64,
    pub store_latency_p99_us: u64,
    pub recall_latency_p99_us: u64,
    pub qps: f64,
    pub total_documents: usize,
    pub total_queries: usize,
}

/// 功能测试指标。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FunctionalMetrics {
    pub store_recall_cycles: usize,
    pub avg_store_latency_us: u64,
    pub avg_recall_latency_us: u64,
    pub dream_duration_ms: u64,
    pub crystallize_duration_ms: u64,
    pub shelf_mount_success: bool,
    pub session_isolation_pass: bool,
    pub dedup_pass: bool,
    pub l0_l5_coverage: usize, // 覆盖的层级数
}

/// 内存使用指标。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MemoryMetrics {
    pub baseline_rss_bytes: u64,
    pub after_load_rss_bytes: u64,
    pub encoder_increment_bytes: u64,
    pub after_1000_docs_rss_bytes: u64,
    pub after_10000_docs_rss_bytes: u64,
    pub after_consolidate_rss_bytes: u64,
    pub leak_test_passed: bool,
    pub leak_growth_bytes: i64,
    pub memory_limit_mb: u64,
    pub within_budget: bool,
}

/// 集成指标。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct IntegrationMetrics {
    pub store_latency_p99_us: u64,
    pub recall_latency_p99_us: u64,
    pub qps: f64,
    pub multi_agent_concurrent: usize,
    pub lru_eviction_works: bool,
    pub agent_isolation_pass: bool,
}

/// 竞品对比数据。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CompetitorResult {
    pub name: String,
    pub dataset: String,
    pub metric_name: String,
    pub metric_value: f64,
    pub source: String,
}

/// 完整基准测试报告。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BenchmarkReport {
    pub version: String,
    pub timestamp: String,
    pub environment: EnvironmentInfo,
    pub retrieval: Option<RetrievalMetrics>,
    pub functional: Option<FunctionalMetrics>,
    pub memory: Option<MemoryMetrics>,
    pub integration: Option<IntegrationMetrics>,
    pub competitor_comparison: Vec<CompetitorResult>,
}

impl BenchmarkReport {
    /// 创建新报告。
    pub fn new(version: &str, encoder: &str) -> Self {
        let now = chrono::Utc::now().to_rfc3339();
        Self {
            version: "0.1.0".to_string(),
            timestamp: now,
            environment: EnvironmentInfo {
                os: std::env::consts::OS.to_string(),
                arch: std::env::consts::ARCH.to_string(),
                rust_version: "stable".to_string(),
                memhop_version: version.to_string(),
                encoder: encoder.to_string(),
                timestamp: chrono::Utc::now().to_rfc3339(),
            },
            retrieval: None,
            functional: None,
            memory: None,
            integration: None,
            competitor_comparison: Vec::new(),
        }
    }

    /// 添加竞品基线数据。
    pub fn add_competitor(&mut self, result: CompetitorResult) {
        self.competitor_comparison.push(result);
    }

    /// 序列化为 JSON 字符串。
    pub fn to_json(&self) -> String {
        serde_json::to_string_pretty(self).unwrap_or_else(|_| "{}".to_string())
    }

    /// 生成 Markdown 格式的对比表格。
    pub fn to_markdown(&self) -> String {
        let mut md = String::new();
        md.push_str(&format!(
            "# MemHop Benchmark Report v{}\n\n",
            self.environment.memhop_version
        ));
        md.push_str(&format!("**Date**: {}\n\n", self.timestamp));
        md.push_str(&format!(
            "**Environment**: {} / {} / Encoder: {}\n\n",
            self.environment.os, self.environment.arch, self.environment.encoder
        ));

        // 检索性能
        if let Some(ref r) = self.retrieval {
            md.push_str("## Retrieval Performance\n\n");
            md.push_str("| Metric | Value |\n|--------|-------|\n");
            md.push_str(&format!("| NDCG@10 | {:.4} |\n", r.ndcg_at_10));
            md.push_str(&format!("| Recall@5 | {:.4} |\n", r.recall_at_5));
            md.push_str(&format!("| Recall@10 | {:.4} |\n", r.recall_at_10));
            md.push_str(&format!("| Precision@10 | {:.4} |\n", r.precision_at_10));
            md.push_str(&format!("| MRR | {:.4} |\n", r.mrr));
            md.push_str(&format!(
                "| Store P99 | {} us |\n",
                r.store_latency_p99_us
            ));
            md.push_str(&format!(
                "| Recall P99 | {} us |\n",
                r.recall_latency_p99_us
            ));
            md.push_str(&format!("| QPS | {:.1} |\n\n", r.qps));
        }

        // 内存使用
        if let Some(ref m) = self.memory {
            md.push_str("## Memory Usage\n\n");
            md.push_str("| Metric | Value |\n|--------|-------|\n");
            md.push_str(&format!(
                "| Baseline RSS | {} |\n",
                format_bytes(m.baseline_rss_bytes)
            ));
            md.push_str(&format!(
                "| After Encoder Load | {} |\n",
                format_bytes(m.after_load_rss_bytes)
            ));
            md.push_str(&format!(
                "| Encoder Increment | {} |\n",
                format_bytes(m.encoder_increment_bytes)
            ));
            md.push_str(&format!(
                "| After 1000 docs | {} |\n",
                format_bytes(m.after_1000_docs_rss_bytes)
            ));
            md.push_str(&format!(
                "| After 10000 docs | {} |\n",
                format_bytes(m.after_10000_docs_rss_bytes)
            ));
            md.push_str(&format!(
                "| Leak Test | {} (growth: {} bytes) |\n\n",
                if m.leak_test_passed {
                    "PASS"
                } else {
                    "FAIL"
                },
                m.leak_growth_bytes
            ));
        }

        // 竞品对比
        if !self.competitor_comparison.is_empty() {
            md.push_str("## Competitor Comparison\n\n");
            md.push_str("| System | Dataset | Metric | Value | Source |\n");
            md.push_str("|--------|---------|--------|-------|--------|\n");
            for c in &self.competitor_comparison {
                md.push_str(&format!(
                    "| {} | {} | {} | {:.4} | {} |\n",
                    c.name, c.dataset, c.metric_name, c.metric_value, c.source
                ));
            }
        }

        md
    }
}

fn format_bytes(bytes: u64) -> String {
    if bytes < 1024 * 1024 {
        format!("{} KB", bytes / 1024)
    } else if bytes < 1024 * 1024 * 1024 {
        format!("{:.1} MB", bytes as f64 / (1024.0 * 1024.0))
    } else {
        format!("{:.2} GB", bytes as f64 / (1024.0 * 1024.0 * 1024.0))
    }
}

/// 内嵌的竞品基线数据。
pub fn load_competitor_baselines() -> Vec<CompetitorResult> {
    vec![
        CompetitorResult {
            name: "FAISS HNSW".to_string(),
            dataset: "BEIR-nfcorpus".to_string(),
            metric_name: "NDCG@10".to_string(),
            metric_value: 0.352,
            source: "FAISS published benchmarks".to_string(),
        },
        CompetitorResult {
            name: "BM25".to_string(),
            dataset: "BEIR-nfcorpus".to_string(),
            metric_name: "NDCG@10".to_string(),
            metric_value: 0.325,
            source: "BEIR leaderboard".to_string(),
        },
        CompetitorResult {
            name: "Mem0".to_string(),
            dataset: "LongMemEval".to_string(),
            metric_name: "QA Accuracy".to_string(),
            metric_value: 0.944,
            source: "Mem0 2026 published".to_string(),
        },
        CompetitorResult {
            name: "EverOS".to_string(),
            dataset: "LongMemEval".to_string(),
            metric_name: "QA Accuracy".to_string(),
            metric_value: 0.830,
            source: "EverOS published".to_string(),
        },
        CompetitorResult {
            name: "Zep".to_string(),
            dataset: "LongMemEval".to_string(),
            metric_name: "QA Accuracy".to_string(),
            metric_value: 0.712,
            source: "Zep published".to_string(),
        },
        CompetitorResult {
            name: "AgentMemory".to_string(),
            dataset: "LongMemEval-S".to_string(),
            metric_name: "R@5".to_string(),
            metric_value: 0.952,
            source: "AgentMemory GitHub".to_string(),
        },
    ]
}
