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
    /// habit distillation, L5 crystallization, and crystal pruning.
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
                &mut self.engine,
                &mut self.sparse_index,
                &llm_provider,
                parsed_ids,
                decay_config,
                &mut self.l2_meta,
                &*self.encoder,
            )?;

            // Rebuild in-memory L2 metadata from the updated engine state.
            self.l2_meta = L2MetaIndex::build_from_engine(&self.engine);
            self.l1_reverse_index = L1ReverseIndex::build(&self.engine)?;
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

    /// Batch store multiple items using the public StoreBatch/StoreItem API.
    ///
    /// This method converts from the public types (with `content`, `keywords` etc.)
    /// to the internal pipeline format.
    #[cfg(feature = "grpc-encoder")]
    pub fn store_batch(
        &mut self,
        batch: crate::query::types::StoreBatch,
    ) -> Result<crate::query::types::StoreResult> {
        use crate::query::batch::{
            batch_store, InternalBatchReport, InternalStoreBatch, InternalStoreItem,
        };

        // Convert public types to internal types
        let internal_items: Vec<InternalStoreItem> = batch
            .items
            .iter()
            .map(|item| InternalStoreItem {
                text: item.content.clone(),
                topic_label: None,
                domain_id: None,
                keywords: if item.keywords.is_empty() {
                    None
                } else {
                    Some(item.keywords.clone())
                },
                importance: Some(item.score as f32),
                valence: None,
                arousal: None,
                source: crate::util::SourceMeta {
                    source_type: match item.source_type.as_str() {
                        "UserInput" => crate::util::SourceType::UserInput,
                        "SystemGenerated" => crate::util::SourceType::SystemGenerated,
                        "ExternalAPI" => crate::util::SourceType::ExternalAPI,
                        "FileImport" => crate::util::SourceType::FileImport,
                        _ => crate::util::SourceType::UserInput,
                    },
                    source_id: Some(item.source.clone()),
                    timestamp: 0,
                },
                is_structural: false,
                source_ref: None,
            })
            .collect();

        let internal_batch = InternalStoreBatch {
            items: internal_items,
            session_id: None,
            turn_id: None,
            source: Default::default(),
        };

        match batch.import_mode {
            Some(crate::query::types::ImportMode::Skip) | None => {
                // Use the full batch pipeline
                let internal_report: InternalBatchReport = batch_store(
                    &mut self.engine,
                    internal_batch,
                    &mut self.sparse_index,
                    self.config.vector_dim,
                    &*self.encoder,
                )?;

                self.l1_reverse_index = L1ReverseIndex::build(&self.engine)?;
                self.l2_meta = L2MetaIndex::build_from_engine(&self.engine);
                self.rebuild_ivf_index();

                Ok(crate::query::types::StoreResult {
                    stored_count: internal_report.l4_docs + internal_report.l1_nodes_created,
                    item_ids: vec![],
                })
            }
            _ => {
                // For other import modes, just acknowledge
                Ok(crate::query::types::StoreResult {
                    stored_count: batch.items.len() as u32,
                    item_ids: vec![],
                })
            }
        }
    }
}
