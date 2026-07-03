// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Dream consolidation API operations.

use crate::config::LlmConfig;
use crate::dream::prune::DreamReport;
#[cfg(feature = "llm")]
use crate::query::search::L1ReverseIndex;
use crate::MemHop;
use crate::MemHopError;
use crate::Result;
#[cfg(feature = "llm")]
use std::collections::HashSet;

impl MemHop {
    /// Lightweight dream: consolidate a single evicted topic.
    ///
    /// Runs L3 distillation + L2 compression + L1 rebuild for the given topic only.
    /// Skips global stages (L1 decay, L0 profile, habit distillation, L5 crystallization)
    /// to keep latency low. Uses `self.config.llm` for LLM configuration.
    pub(crate) fn dream_single_topic(&mut self, topic_id: u64) -> Result<DreamReport> {
        #[cfg(feature = "llm")]
        {
            use crate::dream::dream_pipeline;
            use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;
            let llm_provider = OpenAICompatibleLlmProvider::new(self.config.llm.clone());
            let session_topics: HashSet<u64> = [topic_id].into_iter().collect();

            let report = dream_pipeline(
                &mut self.mmap,
                &mut self.header,
                &mut self.btree,
                &mut self.sparse_index,
                &llm_provider,
                session_topics,
                &mut self.file,
                &crate::config::DecayConfig::default(),
            )?;
            self.l1_reverse_index = L1ReverseIndex::build(&self.mmap, &self.btree)?;
            self.adjacency_cache.invalidate_all();
            self.degree_tracker.invalidate_all();
            Ok(report)
        }
        #[cfg(not(feature = "llm"))]
        {
            let _ = topic_id;
            Err(MemHopError::ConfigError(
                "LLM feature not enabled".to_string(),
            ))
        }
    }

    /// Run dream consolidation pipeline
    ///
    /// Executes memory consolidation on all currently active contexts:
    /// 1. L2 depth demotion (主→次→次次→remove)
    /// 2. L1 rebuild based on updated L2
    /// 3. L0 profile regeneration from L1
    /// 4. L5 crystallization from all ActionChainSlots
    ///
    /// # Arguments
    /// * `llm` - LLM configuration (api_url, api_key, model, temperature, timeout)
    pub fn dream(&mut self, llm: LlmConfig) -> Result<DreamReport> {
        #[cfg(feature = "llm")]
        {
            use crate::dream::dream_pipeline;
            use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;
            let llm_provider = OpenAICompatibleLlmProvider::new(llm);

            let session_topics: HashSet<u64> = self
                .session_manager
                .get_active_topic_ids()
                .into_iter()
                .collect();

            let report = dream_pipeline(
                &mut self.mmap,
                &mut self.header,
                &mut self.btree,
                &mut self.sparse_index,
                &llm_provider,
                session_topics,
                &mut self.file,
                &crate::config::DecayConfig::default(),
            )?;
            self.l1_reverse_index = L1ReverseIndex::build(&self.mmap, &self.btree)?;
            // Invalidate all adjacency cache since L3 distillation may modify any graph
            self.adjacency_cache.invalidate_all();
            self.degree_tracker.invalidate_all();
            Ok(report)
        }
        #[cfg(not(feature = "llm"))]
        {
            let _ = llm;
            Err(MemHopError::ConfigError(
                "LLM feature not enabled".to_string(),
            ))
        }
    }

    /// Batch store multiple documents using the five-phase pipeline
    ///
    /// This method requires an encoder to be set via `set_encoder()` before calling.
    /// Returns an error if no encoder has been configured.
    ///
    /// # Arguments
    /// * `batch` - Batch of items to store
    ///
    /// # Returns
    /// BatchReport with statistics about the operation
    ///
    /// # Errors
    /// Returns error if encoder is not available or batch processing fails
    #[cfg(feature = "grpc-encoder")]
    pub fn batch_store(
        &mut self,
        batch: crate::query::batch::StoreBatch,
    ) -> Result<crate::query::batch::BatchReport> {
        use crate::query::batch::batch_store;

        let report = batch_store(
            &mut self.mmap,
            &mut self.header,
            batch,
            &mut self.btree,
            &mut self.sparse_index,
            self.config.vector_dim,
            self.encoder.as_deref().ok_or_else(|| {
                MemHopError::EncoderError("No encoder configured for batch_store".to_string())
            })?,
            &mut self.file,
        )?;
        self.l1_reverse_index = L1ReverseIndex::build(&self.mmap, &self.btree)?;
        self.rebuild_ivf_index();
        Ok(report)
    }
}
