//! MemHop engine — pure Rust.

pub(crate) mod helpers;
pub(crate) mod store;
pub(crate) mod recall;
pub(crate) mod tree;
pub(crate) mod search;

use crate::encoder::NgramEncoder;
use crate::error::{MemHopError, Result};
use crate::types::{DomainTree, VECTOR_DIM};
use std::collections::{HashMap, HashSet};
use std::sync::{Arc, RwLock};

// ── EngineInner ───────────────────────────────────────────

pub(crate) struct EngineInner {
    pub encoder: NgramEncoder,
    pub storage_path: String,
    pub confidence_threshold: f32,
    pub closed: bool,
    pub dirty_patterns: HashSet<usize>,
    pub trees: HashMap<String, DomainTree>,
    pub default_tree: String,
    /// Number of store() calls since last dream trigger.
    pub store_count_since_dream: usize,
}

// ── MemHop — public API ──────────────────────────────────

pub struct MemHop {
    pub(crate) inner: Arc<RwLock<EngineInner>>,
}

impl MemHop {
    /// Open (or create) a MemHop database at `path`.
    pub fn open(path: &str) -> Result<Self> {
        let inner = EngineInner::open(path)?;
        Ok(MemHop {
            inner: Arc::new(RwLock::new(inner)),
        })
    }

    /// Close the engine, persisting all indices and patterns.
    pub fn close(&self) -> Result<()> {
        let mut engine = self.inner.write().map_err(|e| {
            MemHopError::Internal(format!("lock poisoned: {}", e))
        })?;
        engine.close()
    }

    /// Number of memories across all trees.
    pub fn count(&self) -> usize {
        let engine = self.inner.read().unwrap_or_else(|e| e.into_inner());
        engine.trees.values().map(|t| t.hopfield.len()).sum()
    }

    /// Statistics across all trees.
    pub fn stats(&self) -> HashMap<String, serde_json::Value> {
        let engine = self.inner.read().unwrap_or_else(|e| e.into_inner());
        engine.stats()
    }

    // ── Delegation methods ────────────────────────────────

    pub fn create_tree(&mut self, name: &str) -> Result<()> {
        let mut engine = self.inner.write().map_err(|e| MemHopError::Internal(format!("lock: {}", e)))?;
        engine.create_tree(name)
    }
    pub fn remove_tree(&mut self, name: &str) -> Result<()> {
        let mut engine = self.inner.write().map_err(|e| MemHopError::Internal(format!("lock: {}", e)))?;
        engine.remove_tree(name)
    }
    pub fn list_trees(&self) -> Vec<String> {
        let engine = self.inner.read().unwrap_or_else(|e| e.into_inner());
        engine.list_trees()
    }
    pub fn store(&mut self, text: &str, tree: Option<&str>, opts: &crate::types::StoreOptions) -> Result<String> {
        let mut engine = self.inner.write().map_err(|e| MemHopError::Internal(format!("lock: {}", e)))?;
        engine.store(text, tree, opts)
    }
    pub fn recall(&self, query: &str, tree: Option<&str>) -> Result<Option<crate::types::Memory>> {
        let engine = self.inner.read().map_err(|e| MemHopError::Internal(format!("lock: {}", e)))?;
        engine.recall(query, tree)
    }
    pub fn recall_topk(&self, query: &str, k: usize, tree: Option<&str>) -> Vec<crate::types::Memory> {
        let engine = self.inner.read().unwrap_or_else(|e| e.into_inner());
        engine.recall_topk(query, k, tree)
    }
    pub fn forget(&mut self, memory_id: &str) -> Result<bool> {
        let mut engine = self.inner.write().map_err(|e| MemHopError::Internal(format!("lock: {}", e)))?;
        engine.forget(memory_id)
    }
    pub fn update(&mut self, memory_id: &str, text: Option<&str>, meta: Option<&HashMap<String, serde_json::Value>>) -> Result<bool> {
        let mut engine = self.inner.write().map_err(|e| MemHopError::Internal(format!("lock: {}", e)))?;
        engine.update(memory_id, text, meta)
    }
    pub fn search(&self, filters: &serde_json::Value, limit: usize) -> Result<Vec<crate::types::Memory>> {
        let engine = self.inner.read().map_err(|e| MemHopError::Internal(format!("lock: {}", e)))?;
        let mut criteria_map = HashMap::new();
        if let Some(obj) = filters.as_object() {
            for (k, v) in obj { criteria_map.insert(k.clone(), v.clone()); }
        }
        let criteria = crate::filter::parse_filters(&criteria_map)?;
        engine.search(&criteria, limit)
    }
    pub fn recent(&self, limit: usize, tree: Option<&str>) -> Result<Vec<crate::types::Memory>> {
        let engine = self.inner.read().map_err(|e| MemHopError::Internal(format!("lock: {}", e)))?;
        engine.recent(limit, tree)
    }
    pub fn dream(&mut self, config: Option<&crate::types::DreamConfig>) {
        let dream_cfg = match config {
            Some(cfg) => crate::dream::DreamConfig::from(cfg.clone()),
            None => crate::dream::DreamConfig::default(),
        };
        let inner = self.inner.clone();
        let _ = crate::dream::DreamMode::new(dream_cfg).dream(Some(&inner));
    }
}

impl EngineInner {
    fn open(path: &str) -> Result<Self> {
        let storage_path = path.to_string();
        let default_tree = DomainTree::create(&storage_path, "default")?;

        Ok(EngineInner {
            encoder: NgramEncoder::new(VECTOR_DIM),
            storage_path,
            confidence_threshold: 0.3,
            closed: false,
            dirty_patterns: HashSet::new(),
            trees: {
                let mut m = HashMap::new();
                m.insert("default".to_string(), default_tree);
                m
            },
            default_tree: "default".to_string(),
            store_count_since_dream: 0,
        })
    }

    fn close(&mut self) -> Result<()> {
        if self.closed {
            return Ok(());
        }
        self.closed = true;
        for tree in self.trees.values_mut() {
            tree.storage.close().map_err(MemHopError::from)?;
        }
        Ok(())
    }

    fn stats(&self) -> HashMap<String, serde_json::Value> {
        let mut s = HashMap::new();
        let total: usize = self.trees.values().map(|t| t.hopfield.len()).sum();
        s.insert("total_memories".to_string(), serde_json::Value::Number(total.into()));
        s.insert("tree_count".to_string(), serde_json::Value::Number(self.trees.len().into()));
        s.insert("confidence_threshold".to_string(), serde_json::Value::Number(
            serde_json::Number::from_f64(self.confidence_threshold as f64).unwrap_or(serde_json::Number::from(0)),
        ));
        s
    }

    fn check_closed(&self) -> Result<()> {
        if self.closed {
            return Err(MemHopError::Internal("engine is closed".into()));
        }
        Ok(())
    }

    fn get_tree(&self, name: Option<&str>) -> Result<&DomainTree> {
        let tree_name = name.unwrap_or(&self.default_tree);
        self.trees.get(tree_name).ok_or_else(|| {
            MemHopError::NotFound(format!("tree '{}' not found", tree_name))
        })
    }
}

impl DomainTree {
    fn create(storage_path: &str, name: &str) -> Result<Self> {
        let tree_path = format!("{}/{}", storage_path, name);
        let storage = crate::storage::LmdbStorage::open(&tree_path)
            .map_err(MemHopError::from)?;
        Ok(DomainTree {
            name: name.to_string(),
            hopfield: crate::hopfield::ModernHopfield::new(crate::types::VECTOR_DIM, 8.0),
            sparse_index: crate::index::SparseIndex::new(),
            meta_index: crate::meta_index::MetaIndex::new(),
            storage,
        })
    }
}
