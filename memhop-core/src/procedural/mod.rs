//! 程序性结晶引擎 — 链分析 + 模式提取 + 晶体组装。
//!
//! v0.18.3: 从 L1 超边链中自动提取可复用的操作模式，生成 ProceduralCrystal。
//! 纯算法驱动（无 LLM 依赖）：bigram 文本聚类 + 频率统计。

pub mod pattern;

use crate::brain::Brain;
use crate::engram::Hyperedge;
use crate::error::{MemHopError, Result};
use crate::lmdb::L1Env;
use crate::types::{ChainCluster, CrystallizeReport};
use heed::RoTxn;

/// 遍历 L1 所有超边，筛选链头并收集完整链的 label 序列。
pub(crate) fn analyze_chains(brain: &mut Brain) -> Result<Vec<ChainCluster>> {
    brain.ensure_l1_env()?;
    let l1_env = brain.l1_env.as_ref().unwrap();
    let txn = l1_env
        .env
        .read_txn()
        .map_err(|e| MemHopError::Storage(e.to_string()))?;

    // Step 1: 筛选 chain_label.is_some() && chain_prev.is_none() 的链头
    let mut heads: Vec<String> = Vec::new();
    if let Ok(iter) = l1_env.hyperedges.iter(&txn) {
        for item in iter {
            if let Ok((_key, bytes)) = item
                && let Ok(he) = bincode::deserialize::<Hyperedge>(bytes)
                && he.chain_label.is_some() && he.chain_prev.is_none()
            {
                heads.push(he.id.clone());
            }
        }
    }

    // Step 2: 沿 chain_next 遍历收集完整链的 label 序列
    let chains: Vec<(String, Vec<String>)> = heads
        .iter()
        .map(|head_id| {
            let labels = collect_chain_labels(l1_env, &txn, head_id);
            (head_id.clone(), labels)
        })
        .filter(|(_, labels)| !labels.is_empty())
        .collect();

    // Step 3: bigram 文本聚类
    Ok(pattern::cluster_chain_labels(&chains))
}

/// 沿 chain_next 遍历收集完整链的 label 序列。
fn collect_chain_labels(env: &L1Env, txn: &RoTxn<'_>, start_id: &str) -> Vec<String> {
    let mut labels = Vec::new();
    let mut current_id = Some(start_id.to_string());
    while let Some(ref cid) = current_id {
        match env.hyperedges.get(txn, cid) {
            Ok(Some(bytes)) => {
                if let Ok(he) = bincode::deserialize::<Hyperedge>(bytes) {
                    if let Some(ref label) = he.chain_label {
                        labels.push(label.clone());
                    }
                    current_id = he.chain_next;
                } else {
                    break;
                }
            }
            _ => break,
        }
    }
    labels
}

/// 完整结晶管线：分析 → 聚类 → 步骤提取 → 晶体组装 → 去重存储。
pub fn crystallize(brain: &mut Brain) -> Result<CrystallizeReport> {
    let start = std::time::Instant::now();

    let clusters = analyze_chains(brain)?;
    let chains_analyzed = clusters.iter().map(|c| c.frequency).sum::<u32>();

    // 获取已有晶体用于去重
    let existing = brain.list_crystals()?;
    let existing_labels: Vec<String> = existing.iter().map(|c| c.label.clone()).collect();

    let mut crystals_created = 0u32;

    for cluster in &clusters {
        // 同类链 < 2 条时不结晶
        if cluster.frequency < 2 {
            continue;
        }

        // 去重：若已存在相似 label 的晶体，跳过
        let similar_exists = existing_labels
            .iter()
            .any(|label| label.contains(&cluster.label_pattern) || cluster.label_pattern.contains(label));
        if similar_exists {
            continue;
        }

        // 从链数据提取步骤
        let steps = pattern::extract_steps(brain, &cluster.chain_ids)?;
        if steps.is_empty() {
            continue;
        }

        // 组装晶体
        let crystal = pattern::build_crystal(cluster, steps);

        // 存储
        brain.store_crystal(&crystal)?;
        crystals_created += 1;
    }

    let duration_ms = start.elapsed().as_millis() as u64;

    Ok(CrystallizeReport {
        crystals_created,
        chains_analyzed,
        duration_ms,
    })
}
