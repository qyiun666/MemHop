//! L3 超图邻居扩散 — 沿超边 1-hop BFS 扩散，为检索提供上下文增强。

use std::collections::{HashMap, HashSet};
use crate::domain_graph::L3DomainGraph;
use crate::engram::Hyperedge;
use crate::error::Result;
use crate::storage::store::RedbStore;
use crate::storage::L3_DOMAIN_HYPEREDGES;
use crate::types::NeighborResult;

impl L3DomainGraph {
    /// 从种子节点出发，沿 L3 超边做 BFS 扩散。
    ///
    /// - `seed_ids`: 种子节点 ID 列表（通常为检索命中节点）
    /// - `store`: redb 存储引用，用于读取超边详情
    /// - `max_hops`: 扩散深度（默认 1）
    /// - `max_neighbors`: 返回的最大邻居数
    /// - `seed_scores`: 种子节点 → 原始得分映射（用于计算邻居得分）
    pub fn expand_neighborhood(
        &self,
        seed_ids: &[String],
        store: &RedbStore,
        max_hops: usize,
        max_neighbors: usize,
        seed_scores: &HashMap<String, f32>,
    ) -> Result<Vec<NeighborResult>> {
        if seed_ids.is_empty() || max_hops == 0 || max_neighbors == 0 {
            return Ok(Vec::new());
        }

        let txn = match store.begin_read() {
            Ok(t) => t,
            Err(_) => return Ok(Vec::new()),
        };
        let hyp_table = match txn.open_table(L3_DOMAIN_HYPEREDGES) {
            Ok(t) => t,
            Err(_) => return Ok(Vec::new()),
        };

        let mut visited: HashSet<String> = seed_ids.iter().cloned().collect();
        let mut neighbor_map: HashMap<String, (f32, String)> = HashMap::new();

        // BFS 队列: (node_id, current_hop)
        let mut queue: Vec<(String, usize)> = seed_ids.iter().map(|id| (id.clone(), 0)).collect();
        let mut front = 0;

        while front < queue.len() && neighbor_map.len() < max_neighbors {
            let (current_id, hop) = queue[front].clone();
            front += 1;

            if hop >= max_hops {
                continue;
            }

            // 查找当前节点所属的所有超边
            let hyperedge_ids = match self.node_to_hyperedges.get(&current_id) {
                Some(ids) => ids.clone(),
                None => continue,
            };

            for he_id in &hyperedge_ids {
                // 从存储读取超边详情
                let he: Hyperedge = match hyp_table.get(he_id.as_str()) {
                    Ok(Some(bytes)) => match bincode::deserialize(bytes.value()) {
                        Ok(h) => h,
                        Err(_) => continue,
                    },
                    _ => continue,
                };

                let seed_score = seed_scores.get(&current_id).copied().unwrap_or(0.5);

                for neighbor_id in &he.node_ids {
                    if visited.contains(neighbor_id) {
                        continue;
                    }
                    visited.insert(neighbor_id.clone());

                    // 邻居得分 = 种子得分 × 超边权重 × 衰减系数
                    let decay = 0.5_f32.powi(hop as i32 + 1);
                    let neighbor_score = seed_score * he.weight * decay;

                    // 使用已有的分数或取最大值
                    let entry = neighbor_map.entry(neighbor_id.clone());
                    let (existing_score, _) = entry.or_insert((0.0, String::new()));
                    if neighbor_score > *existing_score {
                        *existing_score = neighbor_score;
                    }

                    if hop + 1 < max_hops {
                        queue.push((neighbor_id.clone(), hop + 1));
                    }

                    if neighbor_map.len() >= max_neighbors {
                        break;
                    }
                }

                if neighbor_map.len() >= max_neighbors {
                    break;
                }
            }
        }

        // 按得分降序排列
        let mut results: Vec<NeighborResult> = neighbor_map
            .into_iter()
            .map(|(node_id, (weight, text))| NeighborResult {
                node_id,
                text,
                weight,
                source_ref: None,
                is_structural: false,
            })
            .collect();
        results.sort_by(|a, b| b.weight.partial_cmp(&a.weight).unwrap_or(std::cmp::Ordering::Equal));
        results.truncate(max_neighbors);

        Ok(results)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::engram::Hyperedge;
    use crate::types::HyperedgeKind;

    #[test]
    fn test_expand_empty_seeds() {
        let graph = L3DomainGraph::new();
        let store_path = std::env::temp_dir().join(format!("test_neighbor_empty_{}", std::process::id()));
        let _ = std::fs::remove_file(&store_path);
        let store = RedbStore::open(&store_path).unwrap();
        
        let result = graph.expand_neighborhood(&[], &store, 1, 5, &HashMap::new()).unwrap();
        assert!(result.is_empty());
        
        let _ = std::fs::remove_file(&store_path);
    }
}
