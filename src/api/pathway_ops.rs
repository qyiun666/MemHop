// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L6 procedural-memory pathway API operations.

use crate::layers::pathway::PathwayWeightSlot;
use crate::MemHop;
use crate::Result;

impl MemHop {
    /// Save (replace) the full set of L6 procedural memory pathway weights.
    pub fn save_pathways(&mut self, pathways: Vec<PathwayWeightSlot>) -> Result<()> {
        self.pathways = pathways;
        Ok(())
    }

    /// Load all L6 pathway weights from memory.
    pub fn load_pathways(&self) -> Result<Vec<PathwayWeightSlot>> {
        Ok(self.pathways.clone())
    }

    /// List L6 pathway weights with optional filters.
    pub fn list_pathways(
        &self,
        source_prefix: Option<&str>,
        min_weight: Option<f32>,
    ) -> Result<Vec<PathwayWeightSlot>> {
        let mut result = Vec::new();
        for pw in &self.pathways {
            if let Some(prefix) = source_prefix {
                if !pw.source_node.starts_with(prefix) {
                    continue;
                }
            }
            if let Some(min) = min_weight {
                if pw.weight < min {
                    continue;
                }
            }
            result.push(pw.clone());
        }
        Ok(result)
    }
}
