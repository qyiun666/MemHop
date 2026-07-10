// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! API-20/21: Diagnostic and inspection operations — health check and statistics.

use crate::query::types::HealthStatus;
use crate::storage::record::*;
use crate::{MemHop, Result};

impl MemHop {
    /// Check the health status of the MemHop instance.
    ///
    /// Reports database integrity, layer entry counts, encoder and IVF index
    /// status, and a list of detected issues.
    pub fn health_check(&self) -> Result<HealthStatus> {
        let db_size_bytes = self.engine.file_size();

        // Count entries per layer by scanning the engine index.
        let mut layer_counts: std::collections::HashMap<String, usize> =
            std::collections::HashMap::new();
        let profile_hash = crate::util::hash_id("profile");

        for (&id_hash, &_offset) in self.engine.iter_index() {
            let Ok(Some((record_type, _))) = self.engine.read_record(id_hash) else {
                continue;
            };
            let key = match record_type {
                t if t == REC_L0_PROFILE => {
                    if id_hash == profile_hash {
                        "l0_profile"
                    } else {
                        continue;
                    }
                }
                t if t == REC_L1_SCENE_NODE => "l1_engram",
                t if t == REC_L2_TOPIC => "l2_topic",
                t if t == REC_L3_GRAPH_SLOT => "l3_knowledge",
                t if t == REC_L4_ARCHIVE => "l4_archive",
                t if t == REC_L5_ACTION_CHAIN => "l5_crystal",
                _ => continue,
            };
            *layer_counts.entry(key.to_string()).or_insert(0) += 1;
        }

        // L6 pathway weights are stored as a serialized blob, count from in-memory cache.
        layer_counts.insert("l6_pathway".to_string(), self.pathways.len());

        #[cfg(feature = "grpc-encoder")]
        let encoder_configured = self.encoder.is_some();
        #[cfg(not(feature = "grpc-encoder"))]
        let encoder_configured = false;

        let ivf_index_built = self.ivf_index.as_ref().is_some_and(|ivf| ivf.k > 0);

        let mut issues = Vec::new();
        if !encoder_configured {
            issues.push("No encoder configured".to_string());
        }
        if !ivf_index_built {
            issues.push("IVF index not built".to_string());
        }

        Ok(HealthStatus {
            ok: issues.is_empty(),
            db_size_bytes,
            layer_counts,
            last_dream_at: None,
            encoder_configured,
            ivf_index_built,
            issues,
        })
    }
}
