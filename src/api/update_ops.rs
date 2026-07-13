// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Update API operations.

use crate::query::types::RequestSource;
use crate::query::types::{UpdateRequest, UpdateResult};
use crate::query::update::InternalUpdateRequest;
use crate::MemHop;
use crate::Result;

impl MemHop {
    /// Update memory with multi-level updates.
    ///
    /// This is a thin orchestration wrapper: validate parameters and delegate to
    /// `query::update::update_memory_internal`, which performs WAL-protected
    /// cross-layer writes.
    pub fn update_memory(&mut self, request: UpdateRequest) -> Result<UpdateResult> {
        use crate::query::update::update_memory_internal;

        if request.id.is_empty() {
            return Err(crate::MemHopError::InvalidQuery(
                "id is required".to_string(),
            ));
        }

        // Convert public UpdateRequest to InternalUpdateRequest
        let dialogue_text = request
            .fields
            .get("dialogue_text")
            .and_then(|v| v.as_str())
            .unwrap_or("")
            .to_string();

        if dialogue_text.is_empty() {
            return Err(crate::MemHopError::InvalidQuery(
                "dialogue_text is required".to_string(),
            ));
        }

        let mut internal_req = InternalUpdateRequest {
            topic_id: request.id,
            dialogue_text,
            summary: request
                .fields
                .get("summary")
                .and_then(|v| v.as_str().map(|s| s.to_string())),
            action_chain: None,
            instant_distill: request
                .fields
                .get("instant_distill")
                .and_then(|v| v.as_u64())
                .unwrap_or(0)
                != 0,
            source: RequestSource::default(),
            scene_id: request
                .fields
                .get("scene_id")
                .and_then(|v| v.as_str().map(|s| s.to_string())),
            user_keywords: request
                .fields
                .get("user_keywords")
                .and_then(|v| serde_json::from_value(v.clone()).ok()),
            agent_keywords: request
                .fields
                .get("agent_keywords")
                .and_then(|v| serde_json::from_value(v.clone()).ok()),
        };

        // Auto-preprocess with LLM when enabled and no keywords provided by caller.
        if self.config.llm_preprocess.preprocess_max_tokens > 0
            && internal_req.user_keywords.is_none()
            && internal_req.agent_keywords.is_none()
        {
            #[cfg(feature = "llm")]
            {
                use crate::dream::llm_preprocess;
                use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;

                let provider = OpenAICompatibleLlmProvider::new(self.config.llm.clone());
                let result = llm_preprocess::preprocess_write_content(
                    &provider,
                    &internal_req.dialogue_text,
                    self.config.llm_preprocess.preprocess_temperature,
                    self.config.llm_preprocess.preprocess_max_tokens,
                )?;
                if !result.keywords.is_empty() {
                    internal_req.user_keywords = Some(result.keywords);
                }
            }
        }

        let result = update_memory_internal(
            &mut self.engine,
            internal_req,
            &mut self.sparse_index,
            &mut self.l2_meta,
            &self.config,
            &*self.encoder,
            Some(&mut self.degree_tracker),
            Some(&mut self.l3_index_map),
        );

        // Rebuild IVF index after mutation (single update may have added vectors)
        self.rebuild_ivf_index();

        // Trigger checkpoint if the buffered WAL has reached the configured interval.
        if result.is_ok() && should_checkpoint(&self.config, 0) {
            if let Err(e) = self.checkpoint() {
                tracing::warn!("Auto-checkpoint failed: {}", e);
            }
        }

        result
    }
}

/// Determine whether the buffered journal should be flushed based on config.
fn should_checkpoint(config: &crate::config::MemHopConfig, buffered_count: usize) -> bool {
    match config.auto_checkpoint_interval {
        None => true,
        Some(0) => false,
        Some(n) => buffered_count as u64 >= n,
    }
}
