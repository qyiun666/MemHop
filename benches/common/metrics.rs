//! IR（信息检索）指标计算模块
//!
//! 提供常用的检索评估指标：
//! - Recall@K: 召回率
//! - MRR: 平均倒数排名
//! - NDCG@K: 归一化折损累积增益
//! - Precision@K: 精确率
//! - LatencyStats: 延迟统计

#![allow(dead_code, unused_imports)]

use std::time::Duration;

/// 延迟统计结果
#[derive(Debug, Clone)]
pub struct LatencyStats {
    /// P50（中位数）
    pub p50: Duration,
    /// P95
    pub p95: Duration,
    /// P99
    pub p99: Duration,
    /// 最大延迟
    pub max: Duration,
    /// 平均延迟
    pub mean: Duration,
}

/// 计算 Recall@K
///
/// Recall@K = (前K个结果中相关文档数) / (总相关文档数)
///
/// # 参数
/// - `retrieved_ids`: 检索到的文档ID列表（按相关性排序）
/// - `relevant_ids`: 真实相关文档ID列表
/// - `k`: 考虑前K个结果
///
/// # 返回
/// 0.0 到 1.0 之间的召回率
pub fn recall_at_k(retrieved_ids: &[&str], relevant_ids: &[&str], k: usize) -> f64 {
    if relevant_ids.is_empty() {
        return 0.0;
    }

    let relevant_set: std::collections::HashSet<&str> = relevant_ids.iter().copied().collect();
    let top_k = retrieved_ids.iter().take(k);
    let hits = top_k.filter(|id| relevant_set.contains(**id)).count();

    hits as f64 / relevant_ids.len() as f64
}

/// 计算 MRR（Mean Reciprocal Rank）
///
/// MRR = 1 / rank，其中 rank 是第一个相关结果的排名
///
/// # 参数
/// - `retrieved_ids`: 检索到的文档ID列表（按相关性排序）
/// - `relevant_ids`: 真实相关文档ID列表
///
/// # 返回
/// 0.0 到 1.0 之间的 MRR 值
pub fn mrr(retrieved_ids: &[&str], relevant_ids: &[&str]) -> f64 {
    if relevant_ids.is_empty() {
        return 0.0;
    }

    let relevant_set: std::collections::HashSet<&str> = relevant_ids.iter().copied().collect();

    for (i, id) in retrieved_ids.iter().enumerate() {
        if relevant_set.contains(*id) {
            return 1.0 / (i + 1) as f64;
        }
    }

    0.0
}

/// 计算 NDCG@K（Normalized Discounted Cumulative Gain）
///
/// 使用二元相关性（相关=1，不相关=0）
///
/// # 参数
/// - `retrieved_ids`: 检索到的文档ID列表（按相关性排序）
/// - `relevant_ids`: 真实相关文档ID列表
/// - `k`: 考虑前K个结果
///
/// # 返回
/// 0.0 到 1.0 之间的 NDCG 值
pub fn ndcg_at_k(retrieved_ids: &[&str], relevant_ids: &[&str], k: usize) -> f64 {
    if relevant_ids.is_empty() {
        return 0.0;
    }

    let relevant_set: std::collections::HashSet<&str> = relevant_ids.iter().copied().collect();

    // 计算 DCG
    let dcg: f64 = retrieved_ids
        .iter()
        .take(k)
        .enumerate()
        .map(|(i, id)| {
            if relevant_set.contains(*id) {
                1.0 / ((i + 2) as f64).log2()
            } else {
                0.0
            }
        })
        .sum();

    // 计算 IDCG（理想排序下的 DCG）
    let ideal_count = std::cmp::min(relevant_ids.len(), k);
    let idcg: f64 = (0..ideal_count)
        .map(|i| 1.0 / ((i + 2) as f64).log2())
        .sum();

    if idcg == 0.0 {
        0.0
    } else {
        dcg / idcg
    }
}

/// 计算 Precision@K
///
/// Precision@K = (前K个结果中相关文档数) / K
///
/// # 参数
/// - `retrieved_ids`: 检索到的文档ID列表（按相关性排序）
/// - `relevant_ids`: 真实相关文档ID列表
/// - `k`: 考虑前K个结果
///
/// # 返回
/// 0.0 到 1.0 之间的精确率
pub fn precision_at_k(retrieved_ids: &[&str], relevant_ids: &[&str], k: usize) -> f64 {
    if k == 0 {
        return 0.0;
    }

    let relevant_set: std::collections::HashSet<&str> = relevant_ids.iter().copied().collect();
    let top_k = retrieved_ids.iter().take(k);
    let hits = top_k.filter(|id| relevant_set.contains(**id)).count();

    hits as f64 / k as f64
}

/// 计算延迟统计
///
/// # 参数
/// - `durations`: 延迟测量值列表
///
/// # 返回
/// LatencyStats 结构体，包含 P50, P95, P99, max, mean
pub fn latency_stats(durations: &[Duration]) -> LatencyStats {
    if durations.is_empty() {
        return LatencyStats {
            p50: Duration::ZERO,
            p95: Duration::ZERO,
            p99: Duration::ZERO,
            max: Duration::ZERO,
            mean: Duration::ZERO,
        };
    }

    let mut sorted: Vec<Duration> = durations.to_vec();
    sorted.sort();

    let len = sorted.len();
    let p50_idx = (len - 1) * 50 / 100;
    let p95_idx = (len - 1) * 95 / 100;
    let p99_idx = (len - 1) * 99 / 100;

    let total: Duration = sorted.iter().sum();

    LatencyStats {
        p50: sorted[p50_idx],
        p95: sorted[p95_idx],
        p99: sorted[p99_idx],
        max: sorted[len - 1],
        mean: total / len as u32,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_recall_at_k() {
        let retrieved = ["a", "b", "c", "d", "e"];
        let relevant = ["b", "d", "f"];

        assert_eq!(recall_at_k(&retrieved, &relevant, 1), 0.0);
        assert_eq!(recall_at_k(&retrieved, &relevant, 2), 1.0 / 3.0);
        assert_eq!(recall_at_k(&retrieved, &relevant, 4), 2.0 / 3.0);
        assert_eq!(recall_at_k(&retrieved, &relevant, 5), 2.0 / 3.0);
    }

    #[test]
    fn test_recall_at_k_empty() {
        let retrieved = ["a", "b"];
        let relevant: Vec<&str> = vec![];
        assert_eq!(recall_at_k(&retrieved, &relevant, 5), 0.0);
    }

    #[test]
    fn test_mrr() {
        let retrieved = ["a", "b", "c", "d", "e"];
        let relevant = ["d"];

        assert_eq!(mrr(&retrieved, &relevant), 1.0 / 4.0);
    }

    #[test]
    fn test_mrr_first() {
        let retrieved = ["a", "b", "c"];
        let relevant = ["a"];

        assert_eq!(mrr(&retrieved, &relevant), 1.0);
    }

    #[test]
    fn test_mrr_not_found() {
        let retrieved = ["a", "b", "c"];
        let relevant = ["x"];

        assert_eq!(mrr(&retrieved, &relevant), 0.0);
    }

    #[test]
    fn test_mrr_empty() {
        let retrieved = ["a", "b"];
        let relevant: Vec<&str> = vec![];
        assert_eq!(mrr(&retrieved, &relevant), 0.0);
    }

    #[test]
    fn test_ndcg_at_k() {
        let retrieved = ["a", "b", "c", "d", "e"];
        let relevant = ["a", "c"];

        // 理想排序: a, c -> DCG = 1/log2(2) + 1/log2(3) = 1.0 + 0.6309 = 1.6309
        // 实际排序: a, b, c -> DCG = 1/log2(2) + 0 + 1/log2(4) = 1.0 + 0 + 0.5 = 1.5
        let ndcg = ndcg_at_k(&retrieved, &relevant, 3);
        assert!((ndcg - 0.92).abs() < 0.01); // 约 0.92
    }

    #[test]
    fn test_ndcg_at_k_perfect() {
        let retrieved = ["a", "b", "c"];
        let relevant = ["a", "b"];

        let ndcg = ndcg_at_k(&retrieved, &relevant, 2);
        assert!((ndcg - 1.0).abs() < 0.0001);
    }

    #[test]
    fn test_ndcg_at_k_empty() {
        let retrieved = ["a", "b"];
        let relevant: Vec<&str> = vec![];
        assert_eq!(ndcg_at_k(&retrieved, &relevant, 5), 0.0);
    }

    #[test]
    fn test_precision_at_k() {
        let retrieved = ["a", "b", "c", "d", "e"];
        let relevant = ["b", "d", "f"];

        assert_eq!(precision_at_k(&retrieved, &relevant, 1), 0.0);
        assert_eq!(precision_at_k(&retrieved, &relevant, 2), 0.5);
        assert_eq!(precision_at_k(&retrieved, &relevant, 4), 0.5);
        assert_eq!(precision_at_k(&retrieved, &relevant, 5), 0.4);
    }

    #[test]
    fn test_precision_at_k_zero() {
        let retrieved = ["a", "b"];
        let relevant = ["a"];
        assert_eq!(precision_at_k(&retrieved, &relevant, 0), 0.0);
    }

    #[test]
    fn test_latency_stats() {
        let durations: Vec<Duration> = (1..=100).map(|i| Duration::from_millis(i)).collect();

        let stats = latency_stats(&durations);
        assert_eq!(stats.p50, Duration::from_millis(50));
        assert_eq!(stats.p95, Duration::from_millis(95));
        assert_eq!(stats.p99, Duration::from_millis(99));
        assert_eq!(stats.max, Duration::from_millis(100));
        assert_eq!(stats.mean, Duration::from_millis(50));
    }

    #[test]
    fn test_latency_stats_empty() {
        let durations: Vec<Duration> = vec![];
        let stats = latency_stats(&durations);
        assert_eq!(stats.p50, Duration::ZERO);
        assert_eq!(stats.max, Duration::ZERO);
    }

    #[test]
    fn test_latency_stats_single() {
        let durations = vec![Duration::from_millis(42)];
        let stats = latency_stats(&durations);
        assert_eq!(stats.p50, Duration::from_millis(42));
        assert_eq!(stats.p95, Duration::from_millis(42));
        assert_eq!(stats.p99, Duration::from_millis(42));
        assert_eq!(stats.max, Duration::from_millis(42));
        assert_eq!(stats.mean, Duration::from_millis(42));
    }
}
