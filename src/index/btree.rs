// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// `BTreeIndex` uses HashMap + Linear Hash internally but keeps the old API name.
// Multi-page bucket layout on disk removes the previous ~254-entry limit.
use std::collections::HashMap;

use crate::util::{PAGE_SIZE, SENTINEL_PAGE_ID};

/// Magic number for the new Linear Hash serialization format.
const HASH_MAGIC: u32 = 0x4D485348; // "MHSH"

/// Size of the usable data area in a 4KB page after the 32-byte file page header.
const PAGE_DATA_SIZE: usize = PAGE_SIZE - 32;

/// Size of the per-bucket page header stored inside page data: [count: u16].
const BUCKET_PAGE_HEADER_SIZE: usize = 2;

/// Size of one key/value entry.
const ENTRY_SIZE: usize = 16;

/// Maximum number of (u64, u64) entries that fit in a single bucket page.
const ENTRIES_PER_PAGE: usize = (PAGE_DATA_SIZE - BUCKET_PAGE_HEADER_SIZE) / ENTRY_SIZE;

/// Target load factor (entries / bucket capacity) used to size the hash table.
const LOAD_FACTOR: f32 = 0.75;

/// Sentinel value indicating no next page in an overflow chain.
pub const EMPTY_PAGE: u32 = SENTINEL_PAGE_ID;

/// `buckets[bucket_index]` is the chain of page data blobs for that bucket.
/// Caller writes 32-byte file page headers and links overflow via `PageHeader.next_page`.
#[derive(Debug, Clone)]
pub struct BTreePageData {
    pub bucket_count: u32,
    pub split_pointer: u32,
    pub buckets: Vec<Vec<Vec<u8>>>,
}

/// Keeps the `BTreeIndex` API; internally uses `HashMap` for O(1) lookups
/// and a Linear Hash layout for durable multi-page storage.
#[derive(Debug, Clone)]
pub struct BTreeIndex {
    map: HashMap<u64, u64>,
    bucket_count: u32,
    /// Next bucket to split when the load factor rises.
    split_pointer: u32,
}

impl BTreeIndex {
    pub fn new() -> Self {
        Self {
            map: HashMap::new(),
            bucket_count: 2,
            split_pointer: 0,
        }
    }

    pub fn insert(&mut self, key: u64, value: u64) {
        self.map.insert(key, value);
    }

    pub fn search(&self, key: u64) -> Option<u64> {
        self.map.get(&key).copied()
    }

    pub fn delete(&mut self, key: u64) -> Option<u64> {
        self.map.remove(&key)
    }

    pub fn remove(&mut self, key: u64) -> Option<u64> {
        self.delete(key)
    }

    // Reserved API
    pub fn contains_key(&self, key: u64) -> bool {
        self.map.contains_key(&key)
    }

    pub fn len(&self) -> usize {
        self.map.len()
    }

    pub fn is_empty(&self) -> bool {
        self.map.is_empty()
    }

    // Reserved API
    pub fn clear(&mut self) {
        self.map.clear();
    }

    /// Iterates in sorted key order for BTreeMap compatibility.
    pub fn iter(&self) -> std::vec::IntoIter<(&u64, &u64)> {
        let mut items: Vec<(&u64, &u64)> = self.map.iter().collect();
        items.sort_by_key(|(k, _)| *k);
        items.into_iter()
    }

    // Reserved API
    pub fn first(&self) -> Option<(&u64, &u64)> {
        self.iter().next()
    }

    // Reserved API
    pub fn last(&self) -> Option<(&u64, &u64)> {
        self.iter().next_back()
    }

    /// Range query: keys in [start, end).
    // Reserved API
    pub fn range(&self, start: u64, end: u64) -> impl Iterator<Item = (&u64, &u64)> {
        self.iter().filter(move |(k, _)| **k >= start && **k < end)
    }

    /// Smallest key >= given key.
    // Reserved API
    pub fn lower_bound(&self, key: u64) -> Option<(&u64, &u64)> {
        self.iter().find(|(k, _)| **k >= key)
    }

    /// Largest key < given key.
    // Reserved API
    pub fn upper_bound(&self, key: u64) -> Option<(&u64, &u64)> {
        self.iter().rev().find(|(k, _)| **k < key)
    }

    pub fn bucket_count(&self) -> u32 {
        self.bucket_count
    }

    pub fn split_pointer(&self) -> u32 {
        self.split_pointer
    }

    /// Self-describing format:
    /// [magic: u32][bucket_count: u32][split_pointer: u32]
    /// per bucket: [page_count: u16] per page: [page_len: u16][page_data]
    pub fn serialize(&self) -> Result<Vec<u8>, String> {
        let page_data = self.serialize_to_pages()?;
        let mut bytes = Vec::new();

        bytes.extend_from_slice(&HASH_MAGIC.to_le_bytes());
        bytes.extend_from_slice(&page_data.bucket_count.to_le_bytes());
        bytes.extend_from_slice(&page_data.split_pointer.to_le_bytes());

        for bucket in &page_data.buckets {
            bytes.extend_from_slice(&(bucket.len() as u16).to_le_bytes());
            for page in bucket {
                bytes.extend_from_slice(&(page.len() as u16).to_le_bytes());
                bytes.extend_from_slice(page);
            }
        }

        Ok(bytes)
    }

    /// Supports both the new Linear Hash format and legacy bincode (< v0.45.0).
    pub fn deserialize(data: &[u8]) -> Result<Self, String> {
        if data.len() >= 12 {
            let magic = u32::from_le_bytes([data[0], data[1], data[2], data[3]]);
            if magic == HASH_MAGIC {
                return Self::deserialize_new(data);
            }
        }
        Self::deserialize_legacy(data)
    }

    /// Each bucket is a chain of pages (4064-byte data regions).
    /// Caller writes file page headers and links overflow via `PageHeader.next_page`.
    pub fn serialize_to_pages(&self) -> Result<BTreePageData, String> {
        let (buckets, bucket_count, split_pointer) = self.build_buckets();
        Ok(BTreePageData {
            bucket_count,
            split_pointer,
            buckets,
        })
    }

    pub fn deserialize_from_pages(
        buckets: &[Vec<Vec<u8>>],
        bucket_count: u32,
        split_pointer: u32,
    ) -> Result<Self, String> {
        let mut map = HashMap::new();
        if bucket_count == 0 {
            return Ok(Self {
                map,
                bucket_count: 2,
                split_pointer: 0,
            });
        }

        for (bucket_idx, bucket) in buckets.iter().enumerate() {
            if bucket_idx >= bucket_count as usize {
                break;
            }
            for page in bucket {
                if page.len() < 2 {
                    continue;
                }
                let count = u16::from_le_bytes([page[0], page[1]]) as usize;
                let expected_len = BUCKET_PAGE_HEADER_SIZE + count * ENTRY_SIZE;
                if page.len() < expected_len {
                    return Err(format!(
                        "Bucket page truncated: expected {} bytes, got {}",
                        expected_len,
                        page.len()
                    ));
                }
                for i in 0..count {
                    let off = BUCKET_PAGE_HEADER_SIZE + i * ENTRY_SIZE;
                    let key = u64::from_le_bytes(
                        page[off..off + 8]
                            .try_into()
                            .map_err(|_| "truncated bucket entry".to_string())?,
                    );
                    let val = u64::from_le_bytes(
                        page[off + 8..off + 16]
                            .try_into()
                            .map_err(|_| "truncated bucket entry".to_string())?,
                    );
                    map.insert(key, val);
                }
            }
        }

        Ok(Self {
            map,
            bucket_count,
            split_pointer,
        })
    }

    /// `base` = buckets at previous level, `split_pointer` = next bucket to split.
    fn bucket_for_key(key: u64, base: u32, split_pointer: u32) -> u32 {
        let mut bucket = key % base as u64;
        if (bucket as u32) < split_pointer {
            bucket = key % (2 * base as u64);
        }
        bucket as u32
    }

    /// Buckets sized for ~LOAD_FACTOR; overflow pages handle skewed distributions.
    fn build_buckets(&self) -> (Vec<Vec<Vec<u8>>>, u32, u32) {
        let entries: Vec<(u64, u64)> = self.map.iter().map(|(k, v)| (*k, *v)).collect();

        if entries.is_empty() {
            let empty_page = vec![0u8; PAGE_DATA_SIZE];
            return (vec![vec![empty_page.clone()]; 2], 2, 0);
        }

        let capacity_per_bucket = ENTRIES_PER_PAGE as f32 * LOAD_FACTOR;
        let target_buckets = ((entries.len() as f32) / capacity_per_bucket)
            .ceil()
            .max(2.0) as u32;

        let mut base = 2u32;
        let mut split_pointer = 0u32;

        while base + split_pointer < target_buckets {
            split_pointer += 1;
            if split_pointer == base {
                split_pointer = 0;
                base *= 2;
            }
        }

        let total_buckets = base + split_pointer;
        let mut buckets: Vec<Vec<(u64, u64)>> = vec![Vec::new(); total_buckets as usize];

        for (k, v) in &entries {
            let b = Self::bucket_for_key(*k, base, split_pointer);
            buckets[b as usize].push((*k, *v));
        }

        let page_buckets: Vec<Vec<Vec<u8>>> = buckets
            .into_iter()
            .map(|bucket| {
                bucket
                    .chunks(ENTRIES_PER_PAGE)
                    .map(Self::encode_bucket_page)
                    .collect()
            })
            .collect();

        (page_buckets, total_buckets, split_pointer)
    }

    fn encode_bucket_page(entries: &[(u64, u64)]) -> Vec<u8> {
        let mut page = Vec::with_capacity(PAGE_DATA_SIZE);
        page.extend_from_slice(&(entries.len() as u16).to_le_bytes());
        for (k, v) in entries {
            page.extend_from_slice(&k.to_le_bytes());
            page.extend_from_slice(&v.to_le_bytes());
        }
        page.resize(PAGE_DATA_SIZE, 0);
        page
    }

    fn deserialize_new(data: &[u8]) -> Result<Self, String> {
        if data.len() < 12 {
            return Err("New-format data too short".to_string());
        }
        let bucket_count = u32::from_le_bytes([data[4], data[5], data[6], data[7]]);
        let split_pointer = u32::from_le_bytes([data[8], data[9], data[10], data[11]]);
        let mut offset = 12;

        let mut buckets: Vec<Vec<Vec<u8>>> = Vec::new();
        for _ in 0..bucket_count {
            if offset + 2 > data.len() {
                return Err("Bucket page count truncated".to_string());
            }
            let page_count = u16::from_le_bytes([data[offset], data[offset + 1]]) as usize;
            offset += 2;

            let mut bucket_pages: Vec<Vec<u8>> = Vec::with_capacity(page_count);
            for _ in 0..page_count {
                if offset + 2 > data.len() {
                    return Err("Page length truncated".to_string());
                }
                let page_len = u16::from_le_bytes([data[offset], data[offset + 1]]) as usize;
                offset += 2;
                if offset + page_len > data.len() {
                    return Err("Page data truncated".to_string());
                }
                bucket_pages.push(data[offset..offset + page_len].to_vec());
                offset += page_len;
            }
            buckets.push(bucket_pages);
        }

        Self::deserialize_from_pages(&buckets, bucket_count, split_pointer)
    }

    /// Legacy single-page bincode format from `BTreeMap<u64, u64>` (< v0.45.0).
    fn deserialize_legacy(data: &[u8]) -> Result<Self, String> {
        if data.len() < 8 {
            return Err("Legacy data too short".to_string());
        }
        let len = u64::from_le_bytes(
            data[0..8]
                .try_into()
                .map_err(|_| "truncated legacy data".to_string())?,
        ) as usize;
        let mut map = HashMap::with_capacity(len);
        let mut offset = 8;
        for _ in 0..len {
            if offset + 16 > data.len() {
                return Err("Legacy data truncated".to_string());
            }
            let key = u64::from_le_bytes(
                data[offset..offset + 8]
                    .try_into()
                    .map_err(|_| "truncated legacy data".to_string())?,
            );
            let val = u64::from_le_bytes(
                data[offset + 8..offset + 16]
                    .try_into()
                    .map_err(|_| "truncated legacy data".to_string())?,
            );
            map.insert(key, val);
            offset += 16;
        }
        Ok(Self {
            map,
            bucket_count: 2,
            split_pointer: 0,
        })
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

        index.insert(300, 3);
        index.insert(100, 1);
        index.insert(200, 2);

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

        assert_eq!(index.lower_bound(20), Some((&20, &2)));
        assert_eq!(index.lower_bound(25), Some((&30, &3)));
        assert_eq!(index.lower_bound(100), None);
    }

    #[test]
    fn test_upper_bound() {
        let mut index = BTreeIndex::new();

        index.insert(10, 1);
        index.insert(20, 2);
        index.insert(30, 3);

        assert_eq!(index.upper_bound(25), Some((&20, &2)));
        assert_eq!(index.upper_bound(20), Some((&10, &1)));
        assert_eq!(index.upper_bound(5), None);
    }

    #[test]
    fn test_large_dataset() {
        let mut index = BTreeIndex::new();

        for i in 0..1000 {
            index.insert(i, i * 10);
        }

        assert_eq!(index.len(), 1000);
        assert_eq!(index.search(0), Some(0));
        assert_eq!(index.search(500), Some(5000));
        assert_eq!(index.search(999), Some(9990));

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

        assert_eq!(index.search(0), None);
        assert_eq!(index.delete(0), None);
        assert_eq!(index.first(), None);
        assert_eq!(index.last(), None);

        index.insert(42, 100);
        assert_eq!(index.first(), Some((&42, &100)));
        assert_eq!(index.last(), Some((&42, &100)));

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

        let removed = index.remove(200);
        assert_eq!(removed, Some(2));
        assert_eq!(index.len(), 2);
        assert_eq!(index.search(200), None);

        let removed = index.remove(999);
        assert_eq!(removed, None);
        assert_eq!(index.len(), 2);
    }

    #[test]
    fn test_serialize_to_pages_multi_bucket() {
        let mut index = BTreeIndex::new();

        for i in 0..1000 {
            index.insert(i, i * 10);
        }

        let page_data = index.serialize_to_pages().unwrap();
        assert!(page_data.bucket_count >= 2);
        assert_eq!(page_data.buckets.len(), page_data.bucket_count as usize);

        let total_pages: usize = page_data.buckets.iter().map(|b| b.len()).sum();
        assert!(total_pages >= 1);

        let deserialized = BTreeIndex::deserialize_from_pages(
            &page_data.buckets,
            page_data.bucket_count,
            page_data.split_pointer,
        )
        .unwrap();
        assert_eq!(deserialized.len(), 1000);
        for i in 0..1000 {
            assert_eq!(deserialized.search(i), Some(i * 10));
        }
    }

    #[test]
    fn test_serialize_to_pages_overflow_chain() {
        let mut index = BTreeIndex::new();

        // All keys hash to bucket 0 (multiples of 2^60), forcing overflow chain.
        let step = 8u64;
        for i in 0..1000 {
            index.insert(i * step, i);
        }

        let page_data = index.serialize_to_pages().unwrap();
        let max_chain_len = page_data.buckets.iter().map(|b| b.len()).max().unwrap_or(0);
        assert!(
            max_chain_len >= 2,
            "expected at least one bucket to overflow into a chain"
        );

        let deserialized = BTreeIndex::deserialize_from_pages(
            &page_data.buckets,
            page_data.bucket_count,
            page_data.split_pointer,
        )
        .unwrap();
        assert_eq!(deserialized.len(), 1000);
        for i in 0..1000 {
            assert_eq!(deserialized.search(i * step), Some(i));
        }
    }

    #[test]
    fn test_legacy_deserialization() {
        // Legacy: u64 length + key/value pairs.
        let mut bytes = Vec::new();
        bytes.extend_from_slice(&3u64.to_le_bytes());
        bytes.extend_from_slice(&100u64.to_le_bytes());
        bytes.extend_from_slice(&1u64.to_le_bytes());
        bytes.extend_from_slice(&200u64.to_le_bytes());
        bytes.extend_from_slice(&2u64.to_le_bytes());
        bytes.extend_from_slice(&300u64.to_le_bytes());
        bytes.extend_from_slice(&3u64.to_le_bytes());

        let index = BTreeIndex::deserialize(&bytes).unwrap();
        assert_eq!(index.len(), 3);
        assert_eq!(index.search(100), Some(1));
        assert_eq!(index.search(200), Some(2));
        assert_eq!(index.search(300), Some(3));
    }
}
