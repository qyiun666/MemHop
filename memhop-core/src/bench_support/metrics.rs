//! IR 指标计算 — NDCG@K, Recall@K, Precision@K, MRR 和延迟统计。

use std::collections::HashSet;
use std::time::Duration;

/// 延迟统计结果。
#[derive(Debug, Clone)]
pub struct LatencyStats {
    pub p50: Duration,
    pub p95: Duration,
    pub p99: Duration,
    pub max: Duration,
    pub mean: Duration,
    pub count: usize,
}

/// NDCG@K 计算。
/// `retrieved`: 检索返回的 ID 列表（已排序）
/// `relevant`: 真实相关文档的 ID 集合
/// `k`: 截断位置
pub fn ndcg_at_k(retrieved: &[String], relevant: &HashSet<String>, k: usize) -> f64 {
    let k = k.min(retrieved.len());
    if k == 0 || relevant.is_empty() {
        return 0.0;
    }

    // DCG
    let dcg: f64 = retrieved
        .iter()
        .take(k)
        .enumerate()
        .map(|(i, id)| {
            if relevant.contains(id) {
                1.0 / ((i + 2) as f64).log2()
            } else {
                0.0
            }
        })
        .sum();

    // Ideal DCG
    let ideal_k = k.min(relevant.len());
    let idcg: f64 = (1..=ideal_k)
        .map(|i| 1.0 / ((i + 1) as f64).log2())
        .sum();

    if idcg == 0.0 {
        0.0
    } else {
        dcg / idcg
    }
}

/// Recall@K 计算。
pub fn recall_at_k(retrieved: &[String], relevant: &HashSet<String>, k: usize) -> f64 {
    if relevant.is_empty() {
        return 0.0;
    }
    let k = k.min(retrieved.len());
    let hits = retrieved
        .iter()
        .take(k)
        .filter(|id| relevant.contains(*id))
        .count();
    hits as f64 / relevant.len() as f64
}

/// Precision@K 计算。
pub fn precision_at_k(retrieved: &[String], relevant: &HashSet<String>, k: usize) -> f64 {
    let k = k.min(retrieved.len());
    if k == 0 {
        return 0.0;
    }
    let hits = retrieved
        .iter()
        .take(k)
        .filter(|id| relevant.contains(*id))
        .count();
    hits as f64 / k as f64
}

/// MRR (Mean Reciprocal Rank) 计算。
pub fn mrr(retrieved: &[String], relevant: &HashSet<String>) -> f64 {
    for (i, id) in retrieved.iter().enumerate() {
        if relevant.contains(id) {
            return 1.0 / ((i + 1) as f64);
        }
    }
    0.0
}

/// 从一组延迟值计算统计信息。
pub fn compute_latency_stats(latencies: &mut [Duration]) -> LatencyStats {
    if latencies.is_empty() {
        return LatencyStats {
            p50: Duration::ZERO,
            p95: Duration::ZERO,
            p99: Duration::ZERO,
            max: Duration::ZERO,
            mean: Duration::ZERO,
            count: 0,
        };
    }

    latencies.sort();
    let len = latencies.len();
    let mean = latencies.iter().sum::<Duration>() / len as u32;

    LatencyStats {
        p50: latencies[len * 50 / 100],
        p95: latencies[len * 95 / 100],
        p99: latencies[len * 99 / 100],
        max: latencies[len - 1],
        mean,
        count: len,
    }
}

/// 计算 QPS (每秒查询数)。
pub fn compute_qps(query_count: usize, total_duration: Duration) -> f64 {
    if total_duration.as_secs_f64() == 0.0 {
        return 0.0;
    }
    query_count as f64 / total_duration.as_secs_f64()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_ndcg_perfect() {
        let retrieved = vec!["a".to_string(), "b".to_string(), "c".to_string()];
        let relevant: HashSet<String> = vec!["a".to_string(), "b".to_string(), "c".to_string()]
            .into_iter()
            .collect();
        let ndcg = ndcg_at_k(&retrieved, &relevant, 3);
        assert!((ndcg - 1.0).abs() < 1e-10, "perfect NDCG should be 1.0");
    }

    #[test]
    fn test_recall_at_k() {
        let retrieved = vec!["a".to_string(), "b".to_string(), "c".to_string()];
        let relevant: HashSet<String> = vec!["a".to_string(), "c".to_string(), "d".to_string()]
            .into_iter()
            .collect();
        // k=2: retrieved top-2 = [a,b], only a is relevant → 1/3
        assert!((recall_at_k(&retrieved, &relevant, 2) - 1.0 / 3.0).abs() < 1e-10);
        // k=3: retrieved top-3 = [a,b,c], a and c are relevant → 2/3
        assert!((recall_at_k(&retrieved, &relevant, 3) - 2.0 / 3.0).abs() < 1e-10);
    }

    #[test]
    fn test_precision_at_k() {
        let retrieved = vec!["a".to_string(), "b".to_string(), "c".to_string()];
        let relevant: HashSet<String> = vec!["a".to_string(), "c".to_string()]
            .into_iter()
            .collect();
        assert!((precision_at_k(&retrieved, &relevant, 3) - 2.0 / 3.0).abs() < 1e-10);
    }

    #[test]
    fn test_mrr() {
        let retrieved = vec!["x".to_string(), "y".to_string(), "a".to_string()];
        let relevant: HashSet<String> = vec!["a".to_string()].into_iter().collect();
        assert!((mrr(&retrieved, &relevant) - 1.0 / 3.0).abs() < 1e-10);
    }

    #[test]
    fn test_latency_stats() {
        let mut latencies: Vec<Duration> = (0..100).map(|i| Duration::from_micros(i * 10)).collect();
        let stats = compute_latency_stats(&mut latencies);
        assert_eq!(stats.count, 100);
        assert!(stats.p50 <= stats.p95);
        assert!(stats.p95 <= stats.p99);
    }
}
