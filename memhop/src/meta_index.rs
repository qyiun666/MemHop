//! MetaIndex: O(1) equality-filter accelerator.
//!
//! Indexes 6 metadata fields (layer, type, domain, session_id, path, parent)
//! for fast candidate set intersection during search/recall scope filtering.
//!
//! Supports serialization for startup index recovery (P0-1).

use serde::{Deserialize, Serialize};
use std::collections::{HashMap, HashSet};

// ── MetaIndex for O(1) equality-filter acceleration ────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub(crate) struct MetaIndex {
    /// field_name → (field_value → set of memory_ids)
    by_layer: HashMap<String, HashSet<String>>,
    by_type: HashMap<String, HashSet<String>>,
    by_domain: HashMap<String, HashSet<String>>,
    by_session_id: HashMap<String, HashSet<String>>,
    by_path: HashMap<String, HashSet<String>>,
    pub(crate) by_parent: HashMap<String, HashSet<String>>,
}

impl MetaIndex {
    pub(crate) fn new() -> Self {
        MetaIndex {
            by_layer: HashMap::new(),
            by_type: HashMap::new(),
            by_domain: HashMap::new(),
            by_session_id: HashMap::new(),
            by_path: HashMap::new(),
            by_parent: HashMap::new(),
        }
    }

    pub(crate) fn add(&mut self, id: &str, meta: &HashMap<String, serde_json::Value>) {
        MetaIndex::insert_to(&mut self.by_layer, "layer", id, meta);
        MetaIndex::insert_to(&mut self.by_type, "type", id, meta);
        MetaIndex::insert_to(&mut self.by_domain, "domain", id, meta);
        MetaIndex::insert_to(&mut self.by_session_id, "session_id", id, meta);
        MetaIndex::insert_to(&mut self.by_path, "path", id, meta);
        MetaIndex::insert_to(&mut self.by_parent, "parent", id, meta);
    }

    pub(crate) fn remove(&mut self, id: &str, meta: &HashMap<String, serde_json::Value>) {
        MetaIndex::remove_from(&mut self.by_layer, "layer", id, meta);
        MetaIndex::remove_from(&mut self.by_type, "type", id, meta);
        MetaIndex::remove_from(&mut self.by_domain, "domain", id, meta);
        MetaIndex::remove_from(&mut self.by_session_id, "session_id", id, meta);
        MetaIndex::remove_from(&mut self.by_path, "path", id, meta);
        MetaIndex::remove_from(&mut self.by_parent, "parent", id, meta);
    }

    #[allow(dead_code)]
    pub(crate) fn update(&mut self, id: &str, old_meta: &HashMap<String, serde_json::Value>, new_meta: &HashMap<String, serde_json::Value>) {
        self.remove(id, old_meta);
        self.add(id, new_meta);
    }

    /// Get candidate IDs matching an equality filter. Returns None if field is not indexed
    /// or value not found (caller should fall back to full scan).
    #[allow(dead_code)]
    pub(crate) fn get_candidates(
        &self,
        layer: Option<&str>,
        r#type: Option<&str>,
        domain: Option<&str>,
        session_id: Option<&str>,
        path: Option<&str>,
        parent: Option<&str>,
    ) -> Option<HashSet<String>> {
        if layer.is_none() && r#type.is_none() && domain.is_none()
            && session_id.is_none() && path.is_none() && parent.is_none() {
            return None;
        }

        let mut result: Option<HashSet<String>> = None;
        for (map, val) in [
            (&self.by_layer, layer),
            (&self.by_type, r#type),
            (&self.by_domain, domain),
            (&self.by_session_id, session_id),
            (&self.by_path, path),
            (&self.by_parent, parent),
        ] {
            if let Some(v) = val {
                let set = map.get(v).cloned().unwrap_or_default();
                result = match result {
                    None => Some(set),
                    Some(r) => Some(r.intersection(&set).cloned().collect()),
                };
            }
        }

        if let Some(ref r) = result
            && r.is_empty() {
                return Some(HashSet::new());
            }
        result
    }

    /// Get all session IDs that have been indexed.
    #[allow(dead_code)]
    pub(crate) fn all_session_ids(&self) -> impl Iterator<Item = &String> {
        self.by_session_id.keys()
    }

    /// Get memory IDs belonging to a specific session.
    #[allow(dead_code)]
    pub(crate) fn session_memory_ids(&self, session_id: &str) -> Option<&HashSet<String>> {
        self.by_session_id.get(session_id)
    }

    pub(crate) fn insert_to(
        map: &mut HashMap<String, HashSet<String>>,
        field: &str, id: &str, meta: &HashMap<String, serde_json::Value>,
    ) {
        if let Some(serde_json::Value::String(v)) = meta.get(field) {
            map.entry(v.clone()).or_default().insert(id.to_string());
        }
    }

    pub(crate) fn remove_from(
        map: &mut HashMap<String, HashSet<String>>,
        field: &str, id: &str, meta: &HashMap<String, serde_json::Value>,
    ) {
        if let Some(serde_json::Value::String(v)) = meta.get(field)
            && let Some(set) = map.get_mut(v) {
                set.remove(id);
                if set.is_empty() {
                    map.remove(v);
                }
            }
    }
}

// ── EngineInner ──────────────────────────────────────────

// ── Index persistence ───────────────────────────────────

impl MetaIndex {
    /// Serialize to bytes for LMDB index snapshot.
    #[allow(dead_code)]
    pub(crate) fn to_bytes(&self) -> Vec<u8> {
        bincode::serialize(self).unwrap_or_default()
    }

    /// Deserialize from bytes. Returns None on any decode error.
    #[allow(dead_code)]
    pub(crate) fn from_bytes(data: &[u8]) -> Option<Self> {
        bincode::deserialize(data).ok()
    }
}
