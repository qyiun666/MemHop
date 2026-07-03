// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Import API operations.

use crate::query::types::{ImportRequest, ImportResult};
use crate::MemHop;
use crate::Result;

impl MemHop {
    /// Import memory into specified layer
    pub fn import_memory(&mut self, request: ImportRequest) -> Result<ImportResult> {
        use crate::query::import::import_memory as impl_fn;
        impl_fn(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &mut self.sparse_index,
            request,
            &mut self.file,
            Some(&mut self.degree_tracker),
            Some(&mut self.l3_index_map),
        )
    }

    /// Build hypergraph edges from file path
    ///
    /// Reads files from the given path, extracts keywords, finds related existing
    /// knowledge nodes via BM25 search, and creates KnowledgeEdge connections between them.
    ///
    /// # Arguments
    /// * `path` - Path to file or directory to analyze
    ///
    /// # Returns
    /// * `Ok(ImportResult)` - Result with created edge IDs
    /// * `Err(MemHopError)` - IO, configuration, or import error
    pub fn build_l3_hypergraph_from_path(
        &mut self,
        path: &std::path::Path,
    ) -> Result<ImportResult> {
        use crate::query::import::build_l3_hypergraph_from_path as impl_fn;
        let result = impl_fn(
            &mut self.mmap,
            &mut self.header,
            &mut self.btree,
            &mut self.sparse_index,
            path,
            &mut self.file,
            Some(&mut self.degree_tracker),
            Some(&mut self.l3_index_map),
        )?;
        // Invalidate all adjacency cache since import may modify any graph
        self.adjacency_cache.invalidate_all();
        self.degree_tracker.invalidate_all();
        Ok(result)
    }
}
