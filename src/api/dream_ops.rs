// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Dream consolidation API operations.

#[cfg(feature = "llm")]
use crate::config::DecayConfig;
use crate::dream::prune::DreamReport;
#[cfg(feature = "llm")]
use crate::index::l2_meta::L2MetaIndex;
#[cfg(any(feature = "llm", feature = "grpc-encoder"))]
use crate::query::search::L1ReverseIndex;
use crate::MemHop;
use crate::MemHopError;
use crate::Result;

impl MemHop {
    /// API-11: Run dream consolidation pipeline.
    ///
    /// Executes the full memory consolidation pipeline on the requested L2 contexts:
    /// L3 distillation, L2 compression, L1 rebuild/decay, L0 profile regeneration,
    /// habit distillation, L5 crystallization, L6 pathway decay, and crystal pruning.
    ///
    /// # Arguments
    /// * `l2_ids` - Optional list of L2 context IDs (16-character hex strings). `None` or
    ///   an empty vector runs the pipeline on all existing L2 contexts.
    ///
    /// # Errors
    /// Returns `MemHopError::InvalidQuery` if any provided ID is not a valid hex u64.
    pub fn dream(&mut self, l2_ids: Option<Vec<String>>) -> Result<DreamReport> {
        #[cfg(feature = "llm")]
        {
            use crate::dream::dream_pipeline;
            use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;

            // Empty vec is treated as full run.
            let l2_ids = match l2_ids {
                Some(ids) if ids.is_empty() => None,
                other => other,
            };

            // Validate and parse hex IDs.
            let parsed_ids: Option<Vec<u64>> = match l2_ids {
                None => None,
                Some(ids) => {
                    let mut parsed = Vec::with_capacity(ids.len());
                    for id in ids {
                        let hash = u64::from_str_radix(&id, 16).map_err(|_| {
                            MemHopError::InvalidQuery(format!("Invalid L2 id: {}", id))
                        })?;
                        parsed.push(hash);
                    }
                    Some(parsed)
                }
            };

            let llm_provider = OpenAICompatibleLlmProvider::new(self.config.llm.clone());
            let default_decay = DecayConfig::default();
            let decay_config = self.config.decay_config.as_ref().unwrap_or(&default_decay);

            let report = dream_pipeline(
                &mut self.mmap,
                &mut self.header,
                &mut self.btree,
                &mut self.sparse_index,
                &llm_provider,
                parsed_ids,
                &mut self.file,
                decay_config,
                &self.l2_meta,
            )?;

            // Rebuild in-memory L2 metadata from the updated mmap state.
            self.l2_meta = L2MetaIndex::build(&self.mmap, &self.btree);
            self.l1_reverse_index = L1ReverseIndex::build(&self.mmap, &self.btree)?;
            // Invalidate caches since graph topology and L2 mappings may have changed.
            self.adjacency_cache.invalidate_all();
            self.degree_tracker.invalidate_all();
            Ok(report)
        }
        #[cfg(not(feature = "llm"))]
        {
            let _ = l2_ids;
            Err(MemHopError::ConfigError(
                "LLM feature not enabled".to_string(),
            ))
        }
    }

    /// Lightweight dream: consolidate a single evicted topic.
    ///
    /// Convenience wrapper around `dream(Some(vec![topic_id]))`.
    pub(crate) fn dream_single_topic(&mut self, topic_id: u64) -> Result<DreamReport> {
        self.dream(Some(vec![format!("{:016x}", topic_id)]))
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
