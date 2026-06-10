use crate::brain::{Brain, PrewarmLayerResult};
use crate::error::Result;
use crate::types::{ConsolidateReport, CrystallizeReport, DreamConfig};
use std::collections::HashMap;

impl Brain {
    /// v0.18.3: 延迟索引重建 + 预热 — 主动加载并重建指定层的索引。
    /// 返回各层节点数和耗时。
    pub fn prewarm(&mut self, layers: &[String]) -> Result<HashMap<String, PrewarmLayerResult>> {
        let mut results = HashMap::new();

        for layer in layers {
            match layer.as_str() {
                "L1" => {
                    let start = std::time::Instant::now();
                    self.ensure_l1()?;
                    let nodes = self.l1.as_ref().map(|l1| l1.node_count()).unwrap_or(0);
                    results.insert(
                        "L1".to_string(),
                        PrewarmLayerResult {
                            nodes,
                            duration_ms: start.elapsed().as_millis() as u64,
                        },
                    );
                }
                "L2" => {
                    let start = std::time::Instant::now();
                    self.ensure_l2()?;
                    let nodes = self.count_l2_topics();
                    results.insert(
                        "L2".to_string(),
                        PrewarmLayerResult {
                            nodes,
                            duration_ms: start.elapsed().as_millis() as u64,
                        },
                    );
                }
                "L3" => {
                    let start = std::time::Instant::now();
                    self.ensure_l3()?;
                    let nodes = self.count_l3_nodes();
                    results.insert(
                        "L3".to_string(),
                        PrewarmLayerResult {
                            nodes,
                            duration_ms: start.elapsed().as_millis() as u64,
                        },
                    );
                }
                "L4" => {
                    let start = std::time::Instant::now();
                    self.ensure_l4()?;
                    let nodes = self.count_l4_docs();
                    results.insert(
                        "L4".to_string(),
                        PrewarmLayerResult {
                            nodes,
                            duration_ms: start.elapsed().as_millis() as u64,
                        },
                    );
                }
                _ => {
                    eprintln!("[brain] prewarm: unknown layer '{}', skipping", layer);
                }
            }
        }

        Ok(results)
    }

    fn count_l2_topics(&self) -> u64 {
        self.redb_store
            .as_ref()
            .and_then(|store| store.l2_topic_count().ok())
            .unwrap_or(0)
    }

    fn count_l3_nodes(&self) -> u64 {
        self.redb_store
            .as_ref()
            .and_then(|store| store.l3_node_count().ok())
            .unwrap_or(0)
    }

    fn count_l4_docs(&self) -> u64 {
        self.redb_store
            .as_ref()
            .and_then(|store| store.l4_doc_count().ok())
            .unwrap_or(0)
    }

    /// 返回各层存储使用率统计
    pub fn storage_stats(&self) -> Vec<crate::types::StorageLayerInfo> {
        // LMDB 已移除，返回空统计
        Vec::new()
    }

    pub fn consolidate(&mut self) -> Result<ConsolidateReport> {
        let config = DreamConfig::default();
        crate::dream::run(self, &config)
    }

    /// v0.18.3: 运行程序性结晶管线。
    pub fn procedural_crystallize(&mut self) -> Result<CrystallizeReport> {
        crate::procedural::crystallize(self)
    }

    pub fn organize_node(&mut self, node_id: &str) -> Result<()> {
        crate::organize::organize_node(self, node_id)
    }
}
