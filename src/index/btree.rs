// B-tree index module
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

/// Simplified B-tree index using BTreeMap for v0.30
/// This provides in-memory indexing with id_hash -> page_ref mapping
/// Persistent storage to disk implemented in v0.34.2+
#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct BTreeIndex {
    /// In-memory index mapping id_hash to page reference
    map: BTreeMap<u64, u64>,
}

impl BTreeIndex {
    /// Create a new empty BTreeIndex
    pub fn new() -> Self {
        Self {
            map: BTreeMap::new(),
        }
    }

    /// Insert a key-value pair into the index
    /// key: id_hash of the document/vector
    /// value: page_ref (page_id or other reference)
    pub fn insert(&mut self, key: u64, value: u64) {
        self.map.insert(key, value);
    }

    /// Search for a value by key
    /// Returns Some(page_ref) if found, None otherwise
    pub fn search(&self, key: u64) -> Option<u64> {
        self.map.get(&key).copied()
    }

    /// Delete a key-value pair from the index
    /// Returns the old value if the key existed
    pub fn delete(&mut self, key: u64) -> Option<u64> {
        self.map.remove(&key)
    }

    /// Remove a key-value pair from the index (alias for delete)
    /// Returns the old value if the key existed
    pub fn remove(&mut self, key: u64) -> Option<u64> {
        self.delete(key)
    }

    /// Check if the index contains a key
    pub fn contains_key(&self, key: u64) -> bool {
        self.map.contains_key(&key)
    }

    /// Get the number of entries in the index
    pub fn len(&self) -> usize {
        self.map.len()
    }

    /// Check if the index is empty
    pub fn is_empty(&self) -> bool {
        self.map.is_empty()
    }

    /// Clear all entries from the index
    pub fn clear(&mut self) {
        self.map.clear();
    }

    /// Iterate over all entries in sorted order
    pub fn iter(&self) -> impl Iterator<Item = (&u64, &u64)> {
        self.map.iter()
    }

    /// Get the first entry (minimum key)
    pub fn first(&self) -> Option<(&u64, &u64)> {
        self.map.first_key_value()
    }

    /// Get the last entry (maximum key)
    pub fn last(&self) -> Option<(&u64, &u64)> {
        self.map.last_key_value()
    }

    /// Range query: get all entries with keys in [start, end)
    pub fn range(&self, start: u64, end: u64) -> impl Iterator<Item = (&u64, &u64)> {
        self.map.range(start..end)
    }

    /// Find the smallest key greater than or equal to the given key
    pub fn lower_bound(&self, key: u64) -> Option<(&u64, &u64)> {
        self.map.range(key..).next()
    }

    /// Find the largest key less than the given key
    pub fn upper_bound(&self, key: u64) -> Option<(&u64, &u64)> {
        self.map.range(..key).next_back()
    }

    /// Serialize B-tree index to binary format using bincode
    pub fn serialize(&self) -> Result<Vec<u8>, String> {
        bincode::serialize(self).map_err(|e| format!("Serialization failed: {}", e))
    }

    /// Deserialize B-tree index from binary format using bincode
    pub fn deserialize(data: &[u8]) -> Result<Self, String> {
        bincode::deserialize(data).map_err(|e| format!("Deserialization failed: {}", e))
    }
}

impl Default for BTreeIndex {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_insert_and_search() {
        let mut index = BTreeIndex::new();

        index.insert(100, 1);
        index.insert(200, 2);
        index.insert(300, 3);

        assert_eq!(index.search(100), Some(1));
        assert_eq!(index.search(200), Some(2));
        assert_eq!(index.search(300), Some(3));
        assert_eq!(index.search(400), None);
    }

    #[test]
    fn test_delete() {
        let mut index = BTreeIndex::new();

        index.insert(100, 1);
        index.insert(200, 2);

        assert_eq!(index.delete(100), Some(1));
        assert_eq!(index.search(100), None);
        assert_eq!(index.search(200), Some(2));
        assert_eq!(index.delete(999), None);
    }

    #[test]
    fn test_update_existing_key() {
        let mut index = BTreeIndex::new();

        index.insert(100, 1);
        assert_eq!(index.search(100), Some(1));

        index.insert(100, 99);
        assert_eq!(index.search(100), Some(99));
    }

    #[test]
    fn test_contains_key() {
        let mut index = BTreeIndex::new();

        index.insert(100, 1);

        assert!(index.contains_key(100));
        assert!(!index.contains_key(200));
    }

    #[test]
    fn test_len_and_is_empty() {
        let mut index = BTreeIndex::new();

        assert!(index.is_empty());
        assert_eq!(index.len(), 0);

        index.insert(100, 1);
        assert!(!index.is_empty());
        assert_eq!(index.len(), 1);

        index.insert(200, 2);
        assert_eq!(index.len(), 2);
    }

    #[test]
    fn test_clear() {
        let mut index = BTreeIndex::new();

        index.insert(100, 1);
        index.insert(200, 2);

        index.clear();

        assert!(index.is_empty());
        assert_eq!(index.len(), 0);
        assert_eq!(index.search(100), None);
    }

    #[test]
    fn test_sorted_order() {
        let mut index = BTreeIndex::new();

        // Insert in random order
        index.insert(300, 3);
        index.insert(100, 1);
        index.insert(200, 2);

        // Iteration should be in sorted order
        let keys: Vec<&u64> = index.iter().map(|(k, _)| k).collect();
        assert_eq!(keys, vec![&100, &200, &300]);
    }

    #[test]
    fn test_first_and_last() {
        let mut index = BTreeIndex::new();

        index.insert(300, 3);
        index.insert(100, 1);
        index.insert(200, 2);

        assert_eq!(index.first(), Some((&100, &1)));
        assert_eq!(index.last(), Some((&300, &3)));
    }

    #[test]
    fn test_range_query() {
        let mut index = BTreeIndex::new();

        for i in 0..10 {
            index.insert(i * 10, i);
        }

        // Range [20, 60) should include 20, 30, 40, 50
        let results: Vec<(&u64, &u64)> = index.range(20, 60).collect();
        assert_eq!(results.len(), 4);
        assert_eq!(results[0], (&20, &2));
        assert_eq!(results[1], (&30, &3));
        assert_eq!(results[2], (&40, &4));
        assert_eq!(results[3], (&50, &5));
    }

    #[test]
    fn test_lower_bound() {
        let mut index = BTreeIndex::new();

        index.insert(10, 1);
        index.insert(20, 2);
        index.insert(30, 3);

        // Exact match
        assert_eq!(index.lower_bound(20), Some((&20, &2)));

        // Between keys
        assert_eq!(index.lower_bound(25), Some((&30, &3)));

        // Beyond max
        assert_eq!(index.lower_bound(100), None);
    }

    #[test]
    fn test_upper_bound() {
        let mut index = BTreeIndex::new();

        index.insert(10, 1);
        index.insert(20, 2);
        index.insert(30, 3);

        // Should return largest key < given key
        assert_eq!(index.upper_bound(25), Some((&20, &2)));
        assert_eq!(index.upper_bound(20), Some((&10, &1)));

        // Below min
        assert_eq!(index.upper_bound(5), None);
    }

    #[test]
    fn test_large_dataset() {
        let mut index = BTreeIndex::new();

        // Insert 1000 entries
        for i in 0..1000 {
            index.insert(i, i * 10);
        }

        assert_eq!(index.len(), 1000);

        // Verify some entries
        assert_eq!(index.search(0), Some(0));
        assert_eq!(index.search(500), Some(5000));
        assert_eq!(index.search(999), Some(9990));

        // Verify sorted order
        let mut prev_key = 0u64;
        for (i, (key, _)) in index.iter().enumerate() {
            if i > 0 {
                assert!(*key > prev_key);
            }
            prev_key = *key;
        }
    }

    #[test]
    fn test_edge_cases() {
        let mut index = BTreeIndex::new();

        // Empty index operations
        assert_eq!(index.search(0), None);
        assert_eq!(index.delete(0), None);
        assert_eq!(index.first(), None);
        assert_eq!(index.last(), None);

        // Single element
        index.insert(42, 100);
        assert_eq!(index.first(), Some((&42, &100)));
        assert_eq!(index.last(), Some((&42, &100)));

        // u64::MAX edge case
        index.insert(u64::MAX, 999);
        assert_eq!(index.search(u64::MAX), Some(999));
        assert_eq!(index.last(), Some((&u64::MAX, &999)));
    }

    #[test]
    fn test_serialize_deserialize_empty() {
        let index = BTreeIndex::new();
        let serialized = index.serialize().unwrap();
        let deserialized = BTreeIndex::deserialize(&serialized).unwrap();

        assert_eq!(deserialized.len(), 0);
        assert!(deserialized.is_empty());
    }

    #[test]
    fn test_serialize_deserialize_with_data() {
        let mut index = BTreeIndex::new();
        index.insert(100, 1);
        index.insert(200, 2);
        index.insert(300, 3);

        let serialized = index.serialize().unwrap();
        let deserialized = BTreeIndex::deserialize(&serialized).unwrap();

        assert_eq!(deserialized.len(), 3);
        assert_eq!(deserialized.search(100), Some(1));
        assert_eq!(deserialized.search(200), Some(2));
        assert_eq!(deserialized.search(300), Some(3));
        assert_eq!(deserialized.search(400), None);
    }

    #[test]
    fn test_serialize_deserialize_large_dataset() {
        let mut index = BTreeIndex::new();

        // Insert 1000 entries
        for i in 0..1000 {
            index.insert(i, i * 10);
        }

        let serialized = index.serialize().unwrap();
        let deserialized = BTreeIndex::deserialize(&serialized).unwrap();

        assert_eq!(deserialized.len(), 1000);
        assert_eq!(deserialized.search(0), Some(0));
        assert_eq!(deserialized.search(500), Some(5000));
        assert_eq!(deserialized.search(999), Some(9990));
    }

    #[test]
    fn test_remove_method() {
        let mut index = BTreeIndex::new();
        
        index.insert(100, 1);
        index.insert(200, 2);
        index.insert(300, 3);
        
        assert_eq!(index.len(), 3);
        
        // Remove existing key
        let removed = index.remove(200);
        assert_eq!(removed, Some(2));
        assert_eq!(index.len(), 2);
        assert_eq!(index.search(200), None);
        
        // Remove non-existing key
        let removed = index.remove(999);
        assert_eq!(removed, None);
        assert_eq!(index.len(), 2);
    }
}
