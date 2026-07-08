// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! API-20/21: Diagnostic and inspection operations — health check and statistics.

use crate::file::page::read_page_header;
use crate::query::types::{HealthStatus, MemHopStats};
use crate::shared::slot_io::decode_page_id;
use crate::util::{PageType, PAGE_SIZE};
use crate::{MemHop, Result};

impl MemHop {
    /// Check the health status of the MemHop instance.
    ///
    /// Reports database integrity, layer entry counts, encoder and IVF index
    /// status, and a list of detected issues.
    pub fn health_check(&self) -> Result<HealthStatus> {
        let db_size_bytes = (self.header.page_count as u64) * PAGE_SIZE as u64;

        // Count entries per layer by scanning the B-tree.
        let mut layer_counts: std::collections::HashMap<String, usize> =
            std::collections::HashMap::new();
        let data: &[u8] = &self.mmap[..];
        let profile_hash = crate::util::hash_id("profile");

        for (&id_hash, &page_ref) in self.btree.iter_unsorted() {
            let page_id = decode_page_id(page_ref);
            if page_id == 0 || page_id >= self.header.page_count {
                continue;
            }
            if let Ok(hdr) = read_page_header(data, page_id) {
                let key = match hdr.page_type {
                    t if t == PageType::Profile as u16 => {
                        if id_hash == profile_hash {
                            "l0_profile"
                        } else {
                            continue;
                        }
                    }
                    t if t == PageType::ContextNode as u16 => "l1_engram",
                    t if t == PageType::Context as u16 => "l2_topic",
                    t if t == PageType::HypergraphSlot as u16 => "l3_knowledge",
                    t if t == PageType::Archive as u16 => "l4_archive",
                    t if t == PageType::ActionChain as u16 => "l5_crystal",
                    _ => continue,
                };
                *layer_counts.entry(key.to_string()).or_insert(0) += 1;
            }
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

    /// Get memory layer statistics.
    ///
    /// Returns a detailed breakdown of all L0-L6 layer entry counts,
    /// database file size, IVF index status, and cache metrics.
    pub fn stats(&self) -> Result<MemHopStats> {
        let db_size_bytes = (self.header.page_count as u64) * PAGE_SIZE as u64;

        let mut l0_profile_exists = false;
        let mut l1_engram_count = 0usize;
        let mut l2_topic_count = 0usize;
        let mut l3_graph_count = 0usize;
        let mut l4_archive_count = 0usize;
        let mut l5_crystal_count = 0usize;

        let data: &[u8] = &self.mmap[..];
        let profile_hash = crate::util::hash_id("profile");

        for (&id_hash, &page_ref) in self.btree.iter_unsorted() {
            let page_id = decode_page_id(page_ref);
            if page_id == 0 || page_id >= self.header.page_count {
                continue;
            }

            if id_hash == profile_hash {
                if let Ok(hdr) = read_page_header(data, page_id) {
                    if hdr.page_type == PageType::Profile as u16 {
                        l0_profile_exists = true;
                    }
                }
            }

            if let Ok(hdr) = read_page_header(data, page_id) {
                match hdr.page_type {
                    t if t == PageType::ContextNode as u16 => l1_engram_count += 1,
                    t if t == PageType::Context as u16 => l2_topic_count += 1,
                    t if t == PageType::HypergraphSlot as u16 => l3_graph_count += 1,
                    t if t == PageType::Archive as u16 => l4_archive_count += 1,
                    t if t == PageType::ActionChain as u16 => l5_crystal_count += 1,
                    _ => {}
                }
            }
        }

        let ivf_cluster_count = self.ivf_index.as_ref().map_or(0, |ivf| ivf.k);

        Ok(MemHopStats {
            l0_profile_exists,
            l1_engram_count,
            l2_topic_count,
            l3_graph_count,
            l4_archive_count,
            l5_crystal_count,
            l6_weight_count: self.pathways.len(),
            db_size_bytes,
            ivf_cluster_count,
            cache_hit_rate: 0.0,
        })
    }
}
