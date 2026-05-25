use crate::engine::EngineInner;
use crate::engine::helpers::f16_to_f32;
use crate::encoder::Encoder;
use crate::error::Result;
use crate::types::Memory;

impl EngineInner {
    pub fn recall(&self, query: &str, tree_name: Option<&str>) -> Result<Option<Memory>> {
        self.check_closed()?;
        let tree = self.get_tree(tree_name)?;
        let encoded = self.encoder.encode(query);
        let query_f32 = f16_to_f32(&encoded.dense);
        let result = tree.hopfield.recall(&query_f32);
        match result {
            Some((id, conf)) if conf >= self.confidence_threshold => {
                self.build_memory(tree, &id, conf)
            }
            _ => Ok(None),
        }
    }

    pub fn recall_topk(&self, query: &str, k: usize, tree_name: Option<&str>) -> Vec<Memory> {
        if self.check_closed().is_err() { return Vec::new(); }
        let tree = match self.get_tree(tree_name) { Ok(t) => t, Err(_) => return Vec::new() };
        let encoded = self.encoder.encode(query);
        let query_f32 = f16_to_f32(&encoded.dense);
        let hits = tree.hopfield.recall_topk(&query_f32, k);
        hits.into_iter()
            .filter_map(|(id, conf)| self.build_memory(tree, &id, conf).ok().flatten())
            .collect()
    }

    fn build_memory(&self, tree: &crate::types::DomainTree, id: &str, confidence: f32) -> Result<Option<Memory>> {
        let meta_rec = match tree.storage.get_meta(id)? { Some(m) => m, None => return Ok(None) };
        let blob = match tree.storage.get_blob(id)? { Some(b) => b, None => return Ok(None) };
        Ok(Some(Memory {
            id: id.to_string(),
            text: blob.text,
            meta: blob.meta,
            confidence,
            created_at: crate::engine::helpers::millis_to_iso(meta_rec.created_at),
            content_type: blob.content_type,
            blob: blob.blob_data,
        }))
    }
}
