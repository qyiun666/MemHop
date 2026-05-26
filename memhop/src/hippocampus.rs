//! L1 Hippocampus — short-term episodic buffer.

//! 
//! The hippocampus is a temporary store for recent episodic engrams.
//! It holds a limited number of entries (default: 500). During Dream's
//! REM-1 phase, entries are consolidated into the neocortex (engrams db).
//! 
//! The hippocampus is persisted to LMDB but maintains an in-memory index
//! of entry IDs for fast iteration during Dream.

use std::collections::VecDeque;

use crate::engram::Engram;
use crate::storage::LmdbStorage;
use crate::error::Result;

/// L1 Hippocampus — short-term memory buffer.
pub struct Hippocampus {
    /// In-memory order tracking (most recent last).
    order: VecDeque<String>,
    /// Maximum capacity.
    capacity: usize,
}

impl Hippocampus {
    /// Create hippocampus with the given capacity.
    #[allow(dead_code)]
    pub fn new(capacity: usize) -> Self {
        Hippocampus {
            order: VecDeque::with_capacity(capacity),
            capacity,
        }
    }

    /// Create hippocampus with default capacity (500).
    #[allow(dead_code)]
    pub fn with_default_capacity() -> Self {
        Hippocampus::new(500)
    }

    /// Rebuild the in-memory order from LMDB at startup.
    pub fn rebuild(storage: &LmdbStorage, capacity: usize) -> Result<Self> {
        let txn = storage.begin_read()?;
        let entries = storage.all_hippocampus_entries(&txn)?;
        txn.commit().map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;

        let order: VecDeque<String> = entries.into_iter().map(|(id, _)| id).collect();
        Ok(Hippocampus { order, capacity })
    }

    /// Store a new engram to hippocampus.
    pub fn store(&mut self, storage: &LmdbStorage, engram: &Engram) -> Result<()> {
        let mut txn = storage.begin_write()?;
        storage.put_hippocampus(&mut txn, &engram.id, engram)?;
        txn.commit().map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;

        self.order.push_back(engram.id.clone());
        if self.order.len() > self.capacity
            && let Some(oldest) = self.order.pop_front()
        {
            let mut txn = storage.begin_write()?;
            let _ = storage.delete_hippocampus(&mut txn, &oldest);
            txn.commit().map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
        }
        Ok(())
    }

    /// Remove a set of entries from hippocampus (during Dream consolidation).
    pub fn remove_batch(&mut self, storage: &LmdbStorage, ids: &[String]) -> Result<()> {
        let mut txn = storage.begin_write()?;
        for id in ids {
            storage.delete_hippocampus(&mut txn, id)?;
        }
        txn.commit().map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
        self.order.retain(|id| !ids.contains(id));
        Ok(())
    }

    /// Get all entries currently in hippocampus (most recent last).
    pub fn all_entries(&self, storage: &LmdbStorage) -> Result<Vec<(String, Engram)>> {
        let txn = storage.begin_read()?;
        let entries = storage.all_hippocampus_entries(&txn)?;
        txn.commit().map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
        Ok(entries)
    }

    /// Get a subset of entries (batch for incremental processing).
    pub fn batch_entries(
        &self,
        storage: &LmdbStorage,
        offset: usize,
        limit: usize,
    ) -> Result<Vec<(String, Engram)>> {
        let batch_ids: Vec<&str> = self
            .order
            .iter()
            .skip(offset)
            .take(limit)
            .map(|s| s.as_str())
            .collect();

        if batch_ids.is_empty() {
            return Ok(Vec::new());
        }

        let txn = storage.begin_read()?;
        let mut entries = Vec::new();
        for id in &batch_ids {
            if let Some(engram) = storage.get_hippocampus(&txn, id)? {
                entries.push((id.to_string(), engram));
            }
        }
        txn.commit().map_err(|e| crate::error::MemHopError::Storage(e.to_string()))?;
        Ok(entries)
    }

    /// Number of entries currently in hippocampus.
    pub fn len(&self) -> usize {
        self.order.len()
    }

    #[allow(dead_code)]
    pub fn is_empty(&self) -> bool {
        self.order.is_empty()
    }
}
