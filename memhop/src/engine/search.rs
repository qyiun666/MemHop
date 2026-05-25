use crate::engine::EngineInner;
use crate::engine::helpers::millis_to_iso;
use crate::error::Result;
use crate::filter::{FilterCriteria, matches_filters};
use crate::types::Memory;

impl EngineInner {
    pub fn search(&self, criteria: &FilterCriteria, limit: usize) -> Result<Vec<Memory>> {
        self.check_closed()?;
        let mut results = Vec::new();
        for tree in self.trees.values() {
            if results.len() >= limit { break; }
            let metas = tree.storage.all_metas()?;
            let mut found = 0;
            for (id, meta) in &metas {
                if found >= limit { break; }
                if let Some(blob) = tree.storage.get_blob(id)?
                    && matches_filters(&blob, meta, criteria) {
                        results.push(Memory {
                            id: id.clone(), text: blob.text, meta: blob.meta,
                            confidence: 0.0, created_at: millis_to_iso(meta.created_at),
                            content_type: blob.content_type, blob: blob.blob_data,
                        });
                        found += 1;
                    }
            }
        }
        Ok(results)
    }

    pub fn recent(&self, limit: usize, tree_name: Option<&str>) -> Result<Vec<Memory>> {
        self.check_closed()?;
        let tree = self.get_tree(tree_name)?;
        let mut metas = tree.storage.all_metas()?;
        metas.sort_by_key(|b| std::cmp::Reverse(b.1.created_at));
        let mut results = Vec::new();
        for (id, meta) in metas.iter().take(limit) {
            if let Some(blob) = tree.storage.get_blob(id)? {
                results.push(Memory {
                    id: id.clone(), text: blob.text, meta: blob.meta,
                    confidence: 0.0, created_at: millis_to_iso(meta.created_at),
                    content_type: blob.content_type, blob: blob.blob_data,
                });
            }
        }
        Ok(results)
    }
}
