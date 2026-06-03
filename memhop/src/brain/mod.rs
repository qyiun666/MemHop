use std::path::Path;

use crate::error::{Result, MemHopError};
use crate::types::{BrainConfig, StoreBatch, BatchReport, RecallRequest, RecallResponse, ConsolidateReport};
use crate::lmdb::{L1Env, L2Env, L3Env, L4Env};
use crate::hypergraph::L1Hypergraph;
use crate::topic_graph::L2TopicGraph;
use crate::domain_graph::L3DomainGraph;
use crate::raw_archive::L4RawArchive;
use crate::encoder::NgramEncoder;

/// MemHop v0.14 Brain — 4 层记忆架构顶层 API。
pub struct Brain {
    pub config: BrainConfig,
    pub l1_env: L1Env,
    pub l2_env: L2Env,
    pub l3_env: L3Env,
    pub l4_env: L4Env,
    pub l1: L1Hypergraph,
    pub l2: L2TopicGraph,
    pub l3: L3DomainGraph,
    pub l4: L4RawArchive,
    pub encoder: NgramEncoder,
}

impl Brain {
    pub fn open(config: BrainConfig) -> Result<Self> {
        let path = Path::new(&config.brains_dir);
        std::fs::create_dir_all(path).ok();

        let l1_env = L1Env::open(&path.join("l1_hypergraph.db"))?;
        let l2_env = L2Env::open(&path.join("l2_topics.db"))?;
        let l3_env = L3Env::open(&path.join("l3_domains.db"))?;
        let l4_env = L4Env::open(&path.join("l4_raw.db"))?;

        let mut l1 = L1Hypergraph::new();
        l1.rebuild_bm25(&l1_env)?;
        let l2 = L2TopicGraph::new();
        let l3 = L3DomainGraph::new();
        let l4 = L4RawArchive::new();
        let encoder = NgramEncoder::new(1024);

        Ok(Brain { config, l1_env, l2_env, l3_env, l4_env, l1, l2, l3, l4, encoder })
    }

    pub fn batch_store(&mut self, batch: StoreBatch) -> Result<BatchReport> {
        crate::batch_store::execute(self, batch)
    }

    pub fn recall(&self, req: &RecallRequest) -> Result<RecallResponse> {
        crate::query_engine::execute(self, req)
    }

    pub fn consolidate(&self) -> Result<ConsolidateReport> {
        Err(MemHopError::Internal("consolidate not yet implemented".into()))
    }

    pub fn config(&self) -> &BrainConfig {
        &self.config
    }
}
