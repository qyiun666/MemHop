//! 基准测试报告生成器模块
//!
//! 提供 JSON 和 Markdown 格式的基准测试报告生成功能。

use serde::{Deserialize, Serialize};
use std::path::Path;

/// 基准测试结果
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BenchResult {
    /// 测试名称
    pub name: String,
    /// 测试时间戳
    pub timestamp: String,
    /// 摄取阶段结果
    pub ingest: IngestResult,
    /// 检索阶段结果
    pub retrieval: RetrievalResult,
    /// QA阶段结果
    pub qa: QaResult,
    /// 规模扩展性结果
    pub scalability: Vec<ScalabilityResult>,
}

/// 摄取阶段结果
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct IngestResult {
    /// 总项目数
    pub total_items: usize,
    /// 摄取耗时（毫秒）
    pub duration_ms: u64,
    /// 吞吐量（items/秒）
    pub throughput: f64,
    /// L1 节点创建数
    pub l1_nodes_created: usize,
    /// L2 主题更新数
    pub l2_topics_updated: usize,
}

/// 检索阶段结果
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RetrievalResult {
    /// 总查询数
    pub total_queries: usize,
    /// Recall@1
    pub recall_at_1: f64,
    /// Recall@5
    pub recall_at_5: f64,
    /// Recall@10
    pub recall_at_10: f64,
    /// MRR
    pub mrr: f64,
    /// NDCG@10
    pub ndcg_at_10: f64,
    /// 平均延迟（毫秒）
    pub avg_latency_ms: f64,
    /// P95 延迟（毫秒）
    pub p95_latency_ms: f64,
    /// P99 延迟（毫秒）
    pub p99_latency_ms: f64,
}

/// QA 阶段结果
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct QaResult {
    /// 总问题数
    pub total_questions: usize,
    /// 平均 LLM 评分
    pub avg_llm_score: f64,
    /// 平均端到端延迟（毫秒）
    pub avg_e2e_latency_ms: f64,
    /// 按类别分组的准确率
    pub accuracy_by_category: std::collections::HashMap<String, f64>,
}

/// 规模扩展性结果
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ScalabilityResult {
    /// 数据规模
    pub size: usize,
    /// 摄取时间（毫秒）
    pub ingest_ms: u64,
    /// 检索平均延迟（毫秒）
    pub retrieval_avg_ms: f64,
    /// 内存使用（MB）
    pub memory_mb: f64,
}

/// 竞品基准
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CompetitorBaseline {
    /// 竞品名称
    pub name: String,
    /// 总分
    pub score: f64,
    /// 测试集名称
    pub test_set: String,
    /// 指标名称
    pub metric: String,
}

/// 生成 JSON 报告
///
/// # 参数
/// - `result`: 基准测试结果
/// - `path`: 输出文件路径
///
/// # 返回
/// 操作结果
pub fn generate_json_report(result: &BenchResult, path: &Path) -> Result<(), Box<dyn std::error::Error>> {
    let json = serde_json::to_string_pretty(result)?;
    std::fs::write(path, json)?;
    Ok(())
}

/// 生成 Markdown 报告
///
/// # 参数
/// - `result`: 基准测试结果
/// - `competitors`: 竞品基准列表
/// - `path`: 输出文件路径
///
/// # 返回
/// 操作结果
pub fn generate_markdown_report(
    result: &BenchResult,
    competitors: &[CompetitorBaseline],
    path: &Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let mut md = String::new();

    // 标题
    md.push_str(&format!("# MemHop 基准测试报告\n\n"));
    md.push_str(&format!("**测试名称**: {}\n", result.name));
    md.push_str(&format!("**测试时间**: {}\n\n", result.timestamp));

    // 概览
    md.push_str("## 概览\n\n");
    md.push_str(&format!("- 总摄取项目数: {}\n", result.ingest.total_items));
    md.push_str(&format!("- 总查询数: {}\n", result.retrieval.total_queries));
    md.push_str(&format!("- 总QA问题数: {}\n\n", result.qa.total_questions));

    // 摄取性能
    md.push_str("## 摄取性能\n\n");
    md.push_str("| 指标 | 值 |\n");
    md.push_str("|------|-----|\n");
    md.push_str(&format!("| 总耗时 | {}ms |\n", result.ingest.duration_ms));
    md.push_str(&format!("| 吞吐量 | {:.1} items/s |\n", result.ingest.throughput));
    md.push_str(&format!("| L1节点创建 | {} |\n", result.ingest.l1_nodes_created));
    md.push_str(&format!("| L2主题更新 | {} |\n\n", result.ingest.l2_topics_updated));

    // 检索性能
    md.push_str("## 检索性能\n\n");
    md.push_str("| 指标 | 值 |\n");
    md.push_str("|------|-----|\n");
    md.push_str(&format!("| Recall@1 | {:.3} |\n", result.retrieval.recall_at_1));
    md.push_str(&format!("| Recall@5 | {:.3} |\n", result.retrieval.recall_at_5));
    md.push_str(&format!("| Recall@10 | {:.3} |\n", result.retrieval.recall_at_10));
    md.push_str(&format!("| MRR | {:.3} |\n", result.retrieval.mrr));
    md.push_str(&format!("| NDCG@10 | {:.3} |\n", result.retrieval.ndcg_at_10));
    md.push_str(&format!("| 平均延迟 | {:.1}ms |\n", result.retrieval.avg_latency_ms));
    md.push_str(&format!("| P95延迟 | {:.1}ms |\n", result.retrieval.p95_latency_ms));
    md.push_str(&format!("| P99延迟 | {:.1}ms |\n\n", result.retrieval.p99_latency_ms));

    // QA 性能
    md.push_str("## QA 性能\n\n");
    md.push_str("| 指标 | 值 |\n");
    md.push_str("|------|-----|\n");
    md.push_str(&format!("| 平均LLM评分 | {:.3} |\n", result.qa.avg_llm_score));
    md.push_str(&format!("| 平均延迟 | {:.1}ms |\n\n", result.qa.avg_e2e_latency_ms));

    // 按类别分组准确率
    if !result.qa.accuracy_by_category.is_empty() {
        md.push_str("### 按类别准确率\n\n");
        md.push_str("| 类别 | 准确率 |\n");
        md.push_str("|------|--------|\n");
        for (category, accuracy) in &result.qa.accuracy_by_category {
            md.push_str(&format!("| {} | {:.3} |\n", category, accuracy));
        }
        md.push_str("\n");
    }

    // 规模扩展性
    if !result.scalability.is_empty() {
        md.push_str("## 规模扩展性\n\n");
        md.push_str("| 数据规模 | 摄取时间 | 检索延迟 | 内存使用 |\n");
        md.push_str("|----------|----------|----------|----------|\n");
        for s in &result.scalability {
            md.push_str(&format!(
                "| {} | {}ms | {:.1}ms | {:.1}MB |\n",
                s.size, s.ingest_ms, s.retrieval_avg_ms, s.memory_mb
            ));
        }
        md.push_str("\n");
    }

    // 竞品对比
    if !competitors.is_empty() {
        md.push_str("## 竞品对比\n\n");
        md.push_str("| 竞品 | 指标 | 测试集 | 得分 |\n");
        md.push_str("|------|------|--------|------|\n");
        for c in competitors {
            md.push_str(&format!(
                "| {} | {} | {} | {:.3} |\n",
                c.name, c.metric, c.test_set, c.score
            ));
        }
        md.push_str("\n");
    }

    // 写入文件
    std::fs::write(path, md)?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    fn create_test_result() -> BenchResult {
        let mut accuracy_by_category = HashMap::new();
        accuracy_by_category.insert("概念".to_string(), 0.85);
        accuracy_by_category.insert("应用".to_string(), 0.78);

        BenchResult {
            name: "测试基准".to_string(),
            timestamp: "2024-01-01 00:00:00".to_string(),
            ingest: IngestResult {
                total_items: 1000,
                duration_ms: 5000,
                throughput: 200.0,
                l1_nodes_created: 50,
                l2_topics_updated: 20,
            },
            retrieval: RetrievalResult {
                total_queries: 100,
                recall_at_1: 0.65,
                recall_at_5: 0.82,
                recall_at_10: 0.89,
                mrr: 0.73,
                ndcg_at_10: 0.78,
                avg_latency_ms: 15.5,
                p95_latency_ms: 45.2,
                p99_latency_ms: 89.3,
            },
            qa: QaResult {
                total_questions: 50,
                avg_llm_score: 0.82,
                avg_e2e_latency_ms: 1250.0,
                accuracy_by_category,
            },
            scalability: vec![
                ScalabilityResult {
                    size: 100,
                    ingest_ms: 500,
                    retrieval_avg_ms: 10.0,
                    memory_mb: 5.2,
                },
                ScalabilityResult {
                    size: 1000,
                    ingest_ms: 5000,
                    retrieval_avg_ms: 15.0,
                    memory_mb: 12.5,
                },
            ],
        }
    }

    #[test]
    fn test_generate_json_report() {
        let result = create_test_result();
        let path = std::env::temp_dir().join("test_report.json");

        let res = generate_json_report(&result, &path);
        assert!(res.is_ok());

        let content = std::fs::read_to_string(&path).unwrap();
        let parsed: BenchResult = serde_json::from_str(&content).unwrap();
        assert_eq!(parsed.name, "测试基准");

        let _ = std::fs::remove_file(&path);
    }

    #[test]
    fn test_generate_markdown_report() {
        let result = create_test_result();
        let competitors = vec![
            CompetitorBaseline {
                name: "竞品A".to_string(),
                score: 0.75,
                test_set: "标准测试集".to_string(),
                metric: "Recall@10".to_string(),
            },
        ];
        let path = std::env::temp_dir().join("test_report.md");

        let res = generate_markdown_report(&result, &competitors, &path);
        assert!(res.is_ok());

        let content = std::fs::read_to_string(&path).unwrap();
        assert!(content.contains("# MemHop 基准测试报告"));
        assert!(content.contains("Recall@10"));
        assert!(content.contains("竞品A"));

        let _ = std::fs::remove_file(&path);
    }

    #[test]
    fn test_markdown_with_empty_competitors() {
        let result = create_test_result();
        let path = std::env::temp_dir().join("test_report_no_competitors.md");

        let res = generate_markdown_report(&result, &[], &path);
        assert!(res.is_ok());

        let content = std::fs::read_to_string(&path).unwrap();
        assert!(!content.contains("竞品对比"));

        let _ = std::fs::remove_file(&path);
    }
}
