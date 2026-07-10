// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! API-10: L6 procedural-memory pathway operations.

use crate::layers::pathway::PathwayWeightSlot;
use crate::query::types::{L6Filter, UpdateL6Fields};
use crate::{MemHop, Result};

impl MemHop {
    // ========================================================================
    // New v0.54 mmap-backed API
    // ========================================================================

    /// Get an L6 pathway weight by ID.
    pub fn get_l6(&self, id: &str) -> Result<Option<PathwayWeightSlot>> {
        crate::query::l6_ops::get_l6(&self.engine, id)
    }

    /// Partially update an L6 pathway weight.
    ///
    /// If `weight_delta` is set in fields, the weight is incremented/decremented
    /// by the delta (clamped to [0.0, 1.0]). Otherwise, the absolute `weight`
    /// value is used.
    pub fn update_l6(&mut self, id: &str, fields: UpdateL6Fields) -> Result<PathwayWeightSlot> {
        if let Some(delta) = fields.weight_delta {
            let updated = crate::query::l6_ops::update_l6_weight(&mut self.engine, id, delta)?;
            self.pathways = crate::query::l6_ops::list_l6(&self.engine, None)?;
            Ok(updated)
        } else {
            let updated = crate::query::l6_ops::update_l6(&mut self.engine, id, fields)?;
            self.pathways = crate::query::l6_ops::list_l6(&self.engine, None)?;
            Ok(updated)
        }
    }

    /// Delete an L6 pathway weight by ID.
    pub fn delete_l6(&mut self, id: &str) -> Result<()> {
        crate::query::l6_ops::delete_l6(&mut self.engine, id)?;
        self.pathways = crate::query::l6_ops::list_l6(&self.engine, None)?;
        Ok(())
    }

    /// List L6 pathway weights with optional filters.
    pub fn list_l6(&self, filter: Option<L6Filter>) -> Result<Vec<PathwayWeightSlot>> {
        crate::query::l6_ops::list_l6(&self.engine, filter)
    }

    /// Batch upsert L6 pathway weights.
    pub fn add_l6(&mut self, slots: Vec<PathwayWeightSlot>) -> Result<usize> {
        let total = crate::query::l6_ops::add_l6(&mut self.engine, slots)?;
        self.pathways = crate::query::l6_ops::list_l6(&self.engine, None)?;
        Ok(total)
    }
}
