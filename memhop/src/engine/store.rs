use std::collections::HashMap;
use crate::engine::EngineInner;
use crate::engine::helpers::{generate_memory_id_with_tree, now_millis, protection_to_u8, f16_to_f32};
use crate::encoder::Encoder;
use crate::error::{MemHopError, Result};
use crate::storage::{BlobRecord, MetaRecord};
use crate::types::{Protection, StoreOptions};

impl EngineInner {
    pub fn store(&mut self, text: &str, tree_name: Option<&str>, opts: &StoreOptions) -> Result<String> {
        self.check_closed()?;
        let tn = tree_name.unwrap_or(&self.default_tree).to_string();
        let id = generate_memory_id_with_tree(&tn);
        let encoded = self.encoder.encode(text);
        let mut meta_map: HashMap<String, serde_json::Value> = HashMap::new();
        meta_map.insert("tree".into(), serde_json::Value::String(tn.clone()));
        let blob = BlobRecord { text: text.to_string(), meta: meta_map, content_type: None, blob_data: None };
        let meta_rec = MetaRecord { created_at: now_millis(), importance: 0.5, protection: protection_to_u8(&Protection::Normal), is_dormant: false, key: None, importance_decay_rate: None };
        let tree = self.trees.get_mut(&tn).ok_or_else(|| MemHopError::NotFound(format!("tree '{}' not found", tn)))?;
        tree.storage.put(&id, &encoded.dense, &blob, &meta_rec)?;
        tree.hopfield.add_pattern(&id, &encoded.dense);
        tree.sparse_index.add(&id, &encoded.sparse);
        tree.meta_index.add(&id, &blob.meta);
        if opts.auto_entangle {
            let q = f16_to_f32(&encoded.dense);
            let hits = tree.hopfield.recall_topk(&q, 5);
            for (h_id, conf) in hits { if h_id != id && conf > 0.5 { link_entangle(&id, &h_id, tree); } }
        }
        self.store_count_since_dream += 1;
        Ok(id)
    }

    pub fn forget(&mut self, memory_id: &str) -> Result<bool> {
        self.check_closed()?;
        let (tn, _) = parse_tree_from_id(memory_id);
        let tree = self.trees.get_mut(&tn).ok_or_else(|| MemHopError::NotFound(format!("tree '{}' not found", tn)))?;
        if tree.storage.get_pattern(memory_id)?.is_none() { return Ok(false); }
        let hide_meta = tree.storage.get_blob(memory_id)?.as_ref().map(|b| b.meta.clone());
        if let Some(m) = hide_meta { tree.meta_index.remove(memory_id, &m); }
        tree.sparse_index.remove(memory_id);
        tree.hopfield.remove_pattern(memory_id);
        tree.storage.delete(memory_id)?;
        Ok(true)
    }

    pub fn update(&mut self, memory_id: &str, text: Option<&str>, meta: Option<&HashMap<String, serde_json::Value>>) -> Result<bool> {
        self.check_closed()?;
        let (tn, _) = parse_tree_from_id(memory_id);
        let tree = self.trees.get_mut(&tn).ok_or_else(|| MemHopError::NotFound(format!("tree '{}' not found", tn)))?;
        let mut blob = match tree.storage.get_blob(memory_id)? { Some(b) => b, None => return Ok(false) };
        if let Some(t) = text { blob.text = t.to_string(); }
        if let Some(m) = meta { for (k, v) in m { blob.meta.insert(k.clone(), v.clone()); } }
        tree.storage.update_blob(memory_id, &blob)?;
        Ok(true)
    }
}

fn link_entangle(from: &str, to: &str, tree: &mut crate::types::DomainTree) {
    if let Ok(Some(mut b)) = tree.storage.get_blob(from) {
        let arr = b.meta.entry("connections".into()).or_insert_with(|| serde_json::Value::Array(Vec::new()));
        if let Some(a) = arr.as_array_mut()
            && !a.iter().any(|c| c.get("id").and_then(|v| v.as_str()) == Some(to)) {
                let mut e = serde_json::Map::new(); e.insert("id".into(), serde_json::Value::String(to.to_string())); e.insert("type".into(), serde_json::Value::String("entangle".into()));
                a.push(serde_json::Value::Object(e));
            }
        let _ = tree.storage.update_blob(from, &b);
    }
}

fn parse_tree_from_id(memory_id: &str) -> (String, String) {
    memory_id.find(":m_").map(|p| (memory_id[..p].to_string(), memory_id.to_string())).unwrap_or_else(|| ("default".into(), memory_id.to_string()))
}
