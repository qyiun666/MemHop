//! 模式提取算法 — bigram 聚类、步骤抽取、关键词生成、晶体组装。
//!
//! 所有函数均为纯数据变换，不直接访问 LMDB（由调用方传入处理后的数据）。

use std::collections::{HashMap, HashSet};

use crate::brain::Brain;
use crate::engram::Hyperedge;
use crate::error::{MemHopError, Result};
use crate::storage::L1_HYPEREDGES;
use crate::types::{ChainCluster, CrystalStep, CrystalType, ProceduralCrystal};
use redb::ReadableTable;

// ── 辅助函数 ────────────────────────────────────────────────

/// 提取字符串中所有 bigram（2 字符连续子串），返回去重集合。
fn bigrams(s: &str) -> HashSet<String> {
    let chars: Vec<char> = s.chars().collect();
    chars.windows(2).map(|w| w.iter().collect()).collect()
}

// ── 公开 API ────────────────────────────────────────────────

/// 对 label 序列进行 bigram 文本聚类。
///
/// `chains` 是一个切片，每个元素为 `(链头超边 ID, Vec<标签>)`。
/// 共享 ≥1 个 bigram 的两条链归为同一类。
/// 返回聚类结果，仅包含 ≥2 条链的簇。
pub(crate) fn cluster_chain_labels(chains: &[(String, Vec<String>)]) -> Vec<ChainCluster> {
    if chains.is_empty() {
        return Vec::new();
    }

    // 为每条链构建 bigram 特征集
    let profiles: Vec<(String, Vec<String>, HashSet<String>)> = chains
        .iter()
        .map(|(head_id, labels)| {
            let bigram_set: HashSet<String> =
                labels.iter().flat_map(|label| bigrams(label)).collect();
            (head_id.clone(), labels.clone(), bigram_set)
        })
        .collect();

    // 贪心聚类：用并查集思想合并共享 bigram 的链
    let n = profiles.len();
    let mut cluster_ids: Vec<usize> = (0..n).collect();
    let mut changed = true;

    while changed {
        changed = false;
        for i in 0..n {
            for j in (i + 1)..n {
                if cluster_ids[i] == cluster_ids[j] {
                    continue;
                }
                if profiles[i].2.intersection(&profiles[j].2).next().is_some() {
                    let old_id = cluster_ids[j];
                    let new_id = cluster_ids[i];
                    for id in &mut cluster_ids {
                        if *id == old_id {
                            *id = new_id;
                        }
                    }
                    changed = true;
                }
            }
        }
    }

    // 按 cluster_id 分组
    let mut cluster_map: HashMap<usize, Vec<usize>> = HashMap::new();
    for (idx, &cid) in cluster_ids.iter().enumerate() {
        cluster_map.entry(cid).or_default().push(idx);
    }

    // 构建 ChainCluster（仅含 ≥2 条链的簇）
    cluster_map
        .into_values()
        .filter(|indices| indices.len() >= 2)
        .map(|indices| {
            let frequency = indices.len() as u32;
            let chain_ids: Vec<String> =
                indices.iter().map(|&i| profiles[i].0.clone()).collect();

            // 取第一条链的 label 序列作为该簇的模式描述
            let label_pattern = profiles[indices[0]].1.join(" → ");

            ChainCluster {
                label_pattern,
                chain_ids,
                frequency,
            }
        })
        .collect()
}

/// 从超边链数据中提取 `CrystalStep` 序列。
///
/// `chain_ids` 是同一簇中所有链头的超边 ID。
/// 遍历每条链，收集所有超边的 label 和 node_ids，
/// 按 label 出现频率排序后生成步骤。
pub fn extract_steps(brain: &mut Brain, chain_ids: &[String]) -> Result<Vec<CrystalStep>> {
    let store = brain.redb_store.as_ref()
        .ok_or_else(|| MemHopError::Storage("redb not available".into()))?;
    let rtxn = store.begin_read()?;
    let table = rtxn.open_table(L1_HYPEREDGES)
        .map_err(|e| MemHopError::Storage(format!("open L1_HYPEREDGES: {}", e)))?;

    // 构建超边索引以便快速链遍历
    let mut he_map: std::collections::HashMap<String, Hyperedge> = std::collections::HashMap::new();
    for result in table.iter()
        .map_err(|e| MemHopError::Storage(format!("iter L1_HYPEREDGES: {}", e)))?
    {
        let (_key, bytes) = result
            .map_err(|e| MemHopError::Storage(format!("iter entry: {}", e)))?;
        if let Ok(he) = bincode::deserialize::<Hyperedge>(bytes.value()) {
            he_map.insert(he.id.clone(), he);
        }
    }
    drop(table);
    drop(rtxn);

    // 收集所有超边的 (label, node_ids) 对
    let mut entries: Vec<(String, Vec<String>)> = Vec::new();
    for head_id in chain_ids {
        let mut current_id = Some(head_id.clone());
        while let Some(ref cid) = current_id {
            if let Some(he) = he_map.get(cid) {
                if let Some(ref label) = he.chain_label {
                    entries.push((label.clone(), he.node_ids.clone()));
                }
                current_id = he.chain_next.clone();
            } else {
                break;
            }
        }
    }

    if entries.is_empty() {
        return Ok(Vec::new());
    }

    // 按 label 分组，统计频率并收集 node_ids
    let mut label_groups: HashMap<String, (u32, Vec<String>)> = HashMap::new();
    for (label, node_ids) in &entries {
        let group = label_groups
            .entry(label.clone())
            .or_insert((0, Vec::new()));
        group.0 += 1;
        group.1.extend(node_ids.iter().cloned());
    }

    // 按频率降序排序
    let mut sorted: Vec<(&String, &(u32, Vec<String>))> = label_groups.iter().collect();
    sorted.sort_by_key(|b| std::cmp::Reverse(b.1 .0));

    // 生成 CrystalStep（仅保留出现 ≥2 次的步骤）
    let steps: Vec<CrystalStep> = sorted
        .iter()
        .enumerate()
        .filter(|(_, (_, (count, _)))| *count >= 2)
        .map(|(order, (label, (_, node_ids)))| CrystalStep {
            order: order as u32,
            action: label.to_string(),
            expected_outcome: None,
            source_node_ids: node_ids.clone(),
        })
        .collect();

    Ok(steps)
}

/// 从步骤中提取触发关键词。
///
/// 从每个 step 的 action 中提取长度 ≥2 的字母数字词段。
pub fn generate_trigger_keywords(steps: &[CrystalStep]) -> Vec<String> {
    let mut keywords: Vec<String> = steps
        .iter()
        .flat_map(|step| {
            step.action
                .chars()
                .filter(|c| c.is_alphanumeric() || c.is_whitespace())
                .collect::<String>()
                .split_whitespace()
                .filter(|w| w.len() >= 2)
                .map(|w| w.to_string())
                .collect::<Vec<String>>()
        })
        .collect();

    // 去重（保持顺序）
    let mut seen = HashSet::new();
    keywords.retain(|k| seen.insert(k.clone()));

    keywords
}

/// 从已聚类的链簇和步骤构建 `ProceduralCrystal`。
///
/// `pattern_type` 默认为 `Sequence`。
pub(crate) fn build_crystal(cluster: &ChainCluster, steps: Vec<CrystalStep>) -> ProceduralCrystal {
    let now = chrono::Utc::now().timestamp_millis();
    let id = format!("crys_{}_{}", now, cluster.frequency);

    let trigger_keywords = generate_trigger_keywords(&steps);

    ProceduralCrystal {
        id,
        label: cluster.label_pattern.clone(),
        pattern_type: CrystalType::Sequence,
        steps,
        trigger_keywords,
        context_conditions: Vec::new(),
        source_chain_ids: cluster.chain_ids.clone(),
        usage_count: 0,
        success_rate: 0.0,
        created_at: now,
        updated_at: now,
        version: 1,
        history: Vec::new(),
    }
}

// ── 单元测试 ────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    /// 空链列表：应返回空聚类结果。
    #[test]
    fn test_empty_chains() {
        let chains: Vec<(String, Vec<String>)> = Vec::new();
        let clusters = cluster_chain_labels(&chains);
        assert!(clusters.is_empty());
    }

    /// 单条链：不应产生任何聚类（需 ≥2 条同类链才形成簇）。
    #[test]
    fn test_single_chain() {
        let chains = vec![(
            "he_1".to_string(),
            vec!["报错".to_string(), "查日志".to_string(), "修复".to_string()],
        )];
        let clusters = cluster_chain_labels(&chains);
        assert!(clusters.is_empty());
    }

    /// 两条相同 label 序列的链：应聚为一类。
    #[test]
    fn test_two_similar_chains() {
        let chains = vec![
            (
                "he_1".to_string(),
                vec!["报错".to_string(), "查日志".to_string(), "修复".to_string()],
            ),
            (
                "he_2".to_string(),
                vec!["报错".to_string(), "查日志".to_string(), "修复".to_string()],
            ),
        ];
        let clusters = cluster_chain_labels(&chains);
        assert_eq!(clusters.len(), 1);
        assert_eq!(clusters[0].frequency, 2);
        assert_eq!(clusters[0].chain_ids.len(), 2);
    }

    /// 两条无关 label 序列的链：不应产生聚类。
    #[test]
    fn test_two_dissimilar_chains() {
        let chains = vec![
            (
                "he_1".to_string(),
                vec!["报错".to_string(), "查日志".to_string()],
            ),
            (
                "he_2".to_string(),
                vec!["吃饭".to_string(), "睡觉".to_string()],
            ),
        ];
        // "报错查日志" 和 "吃饭睡觉" 没有共享 bigram
        let clusters = cluster_chain_labels(&chains);
        assert!(clusters.is_empty());
    }

    /// 三条链，其中两条相似一条不同：应产生一个簇。
    #[test]
    fn test_multi_chain_clustering() {
        let chains = vec![
            (
                "he_1".to_string(),
                vec!["调试".to_string(), "修复".to_string()],
            ),
            (
                "he_2".to_string(),
                vec!["调试".to_string(), "修复".to_string(), "验证".to_string()],
            ),
            (
                "he_3".to_string(),
                vec!["开会".to_string(), "写代码".to_string()],
            ),
        ];
        // "调试修复" 和 "调试修复验证" 共享 bigram "调试"、"试修"、"修复"
        let clusters = cluster_chain_labels(&chains);
        assert_eq!(clusters.len(), 1);
        assert_eq!(clusters[0].frequency, 2);
    }

    /// 测试 generate_trigger_keywords 基本功能。
    #[test]
    fn test_generate_keywords() {
        let steps = vec![
            CrystalStep {
                order: 0,
                action: "复现报错".to_string(),
                expected_outcome: None,
                source_node_ids: vec![],
            },
            CrystalStep {
                order: 1,
                action: "查看日志".to_string(),
                expected_outcome: None,
                source_node_ids: vec![],
            },
        ];
        let keywords = generate_trigger_keywords(&steps);
        assert!(keywords.contains(&"复现报错".to_string()));
        assert!(keywords.contains(&"查看日志".to_string()));
    }

    /// 测试 build_crystal 基本功能。
    #[test]
    fn test_build_crystal() {
        let cluster = ChainCluster {
            label_pattern: "调试 → 修复".to_string(),
            chain_ids: vec!["he_1".to_string(), "he_2".to_string()],
            frequency: 2,
        };
        let steps = vec![
            CrystalStep {
                order: 0,
                action: "调试".to_string(),
                expected_outcome: None,
                source_node_ids: vec![],
            },
            CrystalStep {
                order: 1,
                action: "修复".to_string(),
                expected_outcome: None,
                source_node_ids: vec![],
            },
        ];
        let crystal = build_crystal(&cluster, steps);
        assert_eq!(crystal.label, "调试 → 修复");
        assert_eq!(crystal.steps.len(), 2);
        assert!(matches!(crystal.pattern_type, CrystalType::Sequence));
        assert!(!crystal.id.is_empty());
        assert!(!crystal.trigger_keywords.is_empty());
    }
}
