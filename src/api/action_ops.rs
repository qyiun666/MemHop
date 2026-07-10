// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! API-9: L5 ActionChain CRUD operations.

use crate::layers::action_chain::ChainStatus;
use crate::query::types::{CrystalSummary, UpdateL5Fields};
use crate::{MemHop, Result};

impl MemHop {
    /// Get an L5 action chain by ID.
    pub fn get_l5(&self, id: &str) -> Result<Option<CrystalSummary>> {
        Ok(
            crate::query::l5_ops::get_l5(&self.engine, id)?.map(|c| CrystalSummary {
                id: crate::shared::common::format_hash(c.id_hash),
                title: c.title,
                condition: c.trigger,
                status: match c.status {
                    ChainStatus::Active => "active".to_string(),
                    ChainStatus::Deprecated => "deprecated".to_string(),
                    ChainStatus::Draft => "draft".to_string(),
                },
                trigger_count: c.trigger_count,
                success_rate: c.success_rate,
                last_triggered: if c.last_triggered > 0 {
                    Some(c.last_triggered)
                } else {
                    None
                },
                created_at: c.created_at,
            }),
        )
    }

    /// Partially update an L5 action chain.
    pub fn update_l5(&mut self, id: &str, fields: UpdateL5Fields) -> Result<CrystalSummary> {
        crate::query::l5_ops::update_l5(&mut self.engine, id, fields)?;
        self.get_l5(id)?.ok_or_else(|| {
            crate::MemHopError::Corruption("action chain not found after update".into())
        })
    }

    /// Delete an L5 action chain and all its steps.
    pub fn delete_l5(&mut self, id: &str) -> Result<()> {
        crate::query::l5_ops::delete_l5(&mut self.engine, id)
    }
}
