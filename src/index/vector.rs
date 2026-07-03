// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

use crate::file::free_list::EMPTY_FREE_LIST;
use crate::file::header::FileHeader;
use crate::file::page::{allocate_page, write_page_data, write_page_header, PageHeader};
#[cfg(test)]
use crate::index::btree::BTreeIndex;
#[cfg(test)]
use crate::layers::context_node::ContextNode;
use crate::util::{PageType, PAGE_SIZE};
use crate::{MemHopError, Result as MemHopResult};
use half::f16;
use memmap2::MmapMut;
#[cfg(target_arch = "aarch64")]
use std::arch::aarch64::*;
#[cfg(target_arch = "aarch64")]
use std::arch::is_aarch64_feature_detected;
use std::cmp::Ordering;
use std::collections::HashSet;
use std::fs::File;
use std::io::Result as IoResult;

pub struct VectorPage {
    pub dim: usize,
}

impl VectorPage {
    pub fn new(dim: usize) -> Self {
        Self { dim }
    }

    /// Slot bytes: id_hash(8) + last_access(8) + f16[dim].
    pub fn slot_size(&self) -> usize {
        16 + self.dim * 2
    }

    /// `(PAGE_SIZE - 32) / slot_size`, 32 bytes reserved for page metadata.
    #[cfg(test)]
    pub fn vectors_per_page(&self) -> usize {
        (PAGE_SIZE - 32) / self.slot_size()
    }

    pub fn slot_offset(&self, slot_index: u16) -> usize {
        32 + (slot_index as usize) * self.slot_size()
    }
}

#[cfg(target_arch = "x86_64")]
#[target_feature(enable = "avx2")]
unsafe fn cosine_similarity_avx2(a: &[f16], b: &[f16]) -> f32 {
    use std::arch::x86_64::*;
    let len = a.len();
    assert_eq!(len, b.len(), "Vector lengths must match");
    let mut dot_product = 0.0_f32;
    let mut norm_a = 0.0_f32;
    let mut norm_b = 0.0_f32;
    let mut i = 0;
    while i + 8 <= len {
        let a_vals: [f32; 8] = std::array::from_fn(|j| a[i + j].to_f32());
        let b_vals: [f32; 8] = std::array::from_fn(|j| b[i + j].to_f32());
        let va = _mm256_loadu_ps(a_vals.as_ptr());
        let vb = _mm256_loadu_ps(b_vals.as_ptr());
        let prod = _mm256_mul_ps(va, vb);
        dot_product += _mm256_reduce_add_ps(prod);
        let a_sq = _mm256_mul_ps(va, va);
        let b_sq = _mm256_mul_ps(vb, vb);
        norm_a += _mm256_reduce_add_ps(a_sq);
        norm_b += _mm256_reduce_add_ps(b_sq);
        i += 8;
    }
    while i < len {
        let a_val = a[i].to_f32();
        let b_val = b[i].to_f32();
        dot_product += a_val * b_val;
        norm_a += a_val * a_val;
        norm_b += b_val * b_val;
        i += 1;
    }
    let denom = (norm_a * norm_b).sqrt();
    if denom < 1e-10 {
        0.0
    } else {
        dot_product / denom
    }
}

#[cfg(target_arch = "x86_64")]
#[inline]
unsafe fn _mm256_reduce_add_ps(v: std::arch::x86_64::__m256) -> f32 {
    use std::arch::x86_64::*;
    let hi = _mm256_extractf128_ps(v, 1);
    let lo = _mm256_castps256_ps128(v);
    let sum = _mm_add_ps(hi, lo);
    let shuf = _mm_shuffle_ps(sum, sum, 0x4E);
    let sum = _mm_add_ps(sum, shuf);
    let shuf = _mm_shuffle_ps(sum, sum, 0xB1);
    let sum = _mm_add_ss(sum, shuf);
    _mm_cvtss_f32(sum)
}

#[cfg(target_arch = "aarch64")]
#[target_feature(enable = "neon")]
unsafe fn cosine_similarity_neon(a: &[f16], b: &[f16]) -> f32 {
    let len = a.len();
    assert_eq!(len, b.len(), "Vector lengths must match");
    let mut dot = vdupq_n_f32(0.0);
    let mut norm_a = vdupq_n_f32(0.0);
    let mut norm_b = vdupq_n_f32(0.0);
    let mut i = 0;
    while i + 4 <= len {
        let a_vals: [f32; 4] = std::array::from_fn(|j| a[i + j].to_f32());
        let b_vals: [f32; 4] = std::array::from_fn(|j| b[i + j].to_f32());
        let va = vld1q_f32(a_vals.as_ptr());
        let vb = vld1q_f32(b_vals.as_ptr());
        dot = vfmaq_f32(dot, va, vb);
        norm_a = vfmaq_f32(norm_a, va, va);
        norm_b = vfmaq_f32(norm_b, vb, vb);
        i += 4;
    }
    let mut dot_sum = vaddvq_f32(dot);
    let mut norm_a_sum = vaddvq_f32(norm_a);
    let mut norm_b_sum = vaddvq_f32(norm_b);
    while i < len {
        let a_val = a[i].to_f32();
        let b_val = b[i].to_f32();
        dot_sum += a_val * b_val;
        norm_a_sum += a_val * a_val;
        norm_b_sum += b_val * b_val;
        i += 1;
    }
    let denom = (norm_a_sum * norm_b_sum).sqrt();
    if denom < 1e-10 {
        0.0
    } else {
        dot_sum / denom
    }
}

fn cosine_similarity_fallback(a: &[f16], b: &[f16]) -> f32 {
    assert_eq!(a.len(), b.len(), "Vector lengths must match");
    let mut dot_product = 0.0_f32;
    let mut norm_a = 0.0_f32;
    let mut norm_b = 0.0_f32;
    for i in 0..a.len() {
        let a_val = a[i].to_f32();
        let b_val = b[i].to_f32();
        dot_product += a_val * b_val;
        norm_a += a_val * a_val;
        norm_b += b_val * b_val;
    }
    let denom = (norm_a * norm_b).sqrt();
    if denom < 1e-10 {
        0.0
    } else {
        dot_product / denom
    }
}

/// Dispatches to AVX2 / NEON / scalar based on runtime feature detection.
pub fn cosine_similarity(a: &[f16], b: &[f16]) -> f32 {
    assert_eq!(a.len(), b.len());
    #[cfg(target_arch = "x86_64")]
    {
        if is_x86_feature_detected!("avx2") && a.len() >= 8 {
            return unsafe { cosine_similarity_avx2(a, b) };
        }
    }
    #[cfg(target_arch = "aarch64")]
    {
        if is_aarch64_feature_detected!("neon") && a.len() >= 4 {
            return unsafe { cosine_similarity_neon(a, b) };
        }
    }
    cosine_similarity_fallback(a, b)
}

#[cfg(test)]
/// Returns top-k `(id_hash, cosine_score)` pairs by scanning all btree entries.
pub fn brute_force_knn(
    data: &[u8],
    query_vector: &[f16],
    btree: &BTreeIndex,
    vector_dim: usize,
    _page_count: u32,
    k: usize,
) -> MemHopResult<Vec<(u64, f32)>> {
    let mut candidates: Vec<(u64, f32)> = Vec::new();
    for (&id_hash, &page_ref) in btree.iter() {
        let node_page_id = (page_ref >> 16) as u32;
        let node_offset = (node_page_id as usize) * PAGE_SIZE + 32;
        if node_offset >= data.len() {
            continue;
        }
        if let Ok(node) = ContextNode::deserialize(&data[node_offset..]) {
            if node.vector_page_ref == 0 {
                continue;
            }
            let vec_page_id = (node.vector_page_ref >> 16) as u32;
            let vec_slot_index = (node.vector_page_ref & 0xFFFF) as u16;
            if let Ok(vector) = read_vector(data, vec_page_id, vec_slot_index, vector_dim) {
                let score = cosine_similarity(query_vector, &vector);
                candidates.push((id_hash, score));
            }
        }
    }
    candidates.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(Ordering::Equal));
    candidates.truncate(k);
    Ok(candidates)
}

/// Slot layout: `[id_hash: u64][last_access: i64][vector: f16[dim]]`.
pub fn write_vector(
    mmap: &mut MmapMut,
    page_id: u32,
    slot_index: u16,
    id_hash: u64,
    vector: &[f16],
    dim: usize,
) -> IoResult<()> {
    let page = VectorPage::new(dim);
    let offset = page_id as usize * PAGE_SIZE + page.slot_offset(slot_index);
    let slot_size = page.slot_size();
    if offset + slot_size > mmap.len() {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidInput,
            "Write would exceed mmap bounds",
        ));
    }
    mmap[offset..offset + 8].copy_from_slice(&id_hash.to_le_bytes());
    let last_access = 0i64;
    mmap[offset + 8..offset + 16].copy_from_slice(&last_access.to_le_bytes());
    let vector_start = offset + 16;
    for (i, &val) in vector.iter().enumerate() {
        let bytes = val.to_le_bytes();
        mmap[vector_start + i * 2..vector_start + i * 2 + 2].copy_from_slice(&bytes);
    }
    Ok(())
}

pub fn read_vector(data: &[u8], page_id: u32, slot_index: u16, dim: usize) -> IoResult<Vec<f16>> {
    let page = VectorPage::new(dim);
    let offset = page_id as usize * PAGE_SIZE + page.slot_offset(slot_index);
    let slot_size = page.slot_size();
    if offset + slot_size > data.len() {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidInput,
            "Read would exceed data bounds",
        ));
    }
    let vector_start = offset + 16;
    let mut vector = Vec::with_capacity(dim);
    for i in 0..dim {
        let bytes: [u8; 2] = [data[vector_start + i * 2], data[vector_start + i * 2 + 1]];
        vector.push(f16::from_le_bytes(bytes));
    }
    Ok(vector)
}

// ============================================================================
// IVF (Inverted File Index)
// ============================================================================

/// Offsets within `FileHeader.reserved` for IVF metadata.
/// Linear Hash B-tree uses the first 12 bytes; IVF starts at offset 12.
const IVF_CLUSTER_ROOT_OFFSET: usize = 12;
const IVF_BUCKET_ROOT_OFFSET: usize = 16;
const IVF_DIM_OFFSET: usize = 20;
const IVF_K_OFFSET: usize = 22;
const IVF_CLUSTER_MAGIC: &[u8; 4] = b"MHIV";
const IVF_BUCKET_MAGIC: &[u8; 4] = b"MHIB";
const IVF_VERSION: u8 = 1;

type IvfBucketRecord = (u64, u32, u16);
type IvfBuckets = Vec<Vec<IvfBucketRecord>>;

/// In-memory IVF index.
///
/// Centroids and buckets are persisted as chained `IVFCluster` / `IVFBucket`
/// pages. The original `VectorPage` storage is untouched.
pub struct IVFIndex {
    pub centroids: Vec<Vec<f16>>,
    /// `(id_hash, vec_page_id, vec_slot_index)` per centroid.
    pub buckets: IvfBuckets,
    pub dim: usize,
    pub initial_k: usize,
    pub k: usize,
    /// Running sum per centroid for incremental mean (avoids FP drift).
    centroid_sums: Vec<Vec<f32>>,
    counts: Vec<usize>,
}

impl IVFIndex {
    pub fn new(dim: usize, initial_k: usize) -> Self {
        let cap = initial_k.max(1);
        Self {
            centroids: Vec::with_capacity(cap),
            buckets: Vec::with_capacity(cap),
            dim,
            initial_k,
            k: 0,
            centroid_sums: Vec::with_capacity(cap),
            counts: Vec::with_capacity(cap),
        }
    }

    #[cfg(test)]
    pub fn len(&self) -> usize {
        self.buckets.iter().map(|b| b.len()).sum()
    }

    /// Add a vector to the nearest bucket. New centroids are created until
    /// `initial_k` is reached; afterwards incremental mean update is used.
    pub fn add_vector(
        &mut self,
        id_hash: u64,
        vector: &[f16],
        vec_page_id: u32,
        vec_slot_index: u16,
    ) {
        assert_eq!(vector.len(), self.dim, "vector dimension mismatch");
        let idx = if self.centroids.is_empty() || self.centroids.len() < self.initial_k {
            let i = self.centroids.len();
            self.centroids.push(vector.to_vec());
            self.centroid_sums
                .push(vector.iter().map(|x| x.to_f32()).collect());
            self.counts.push(1);
            self.buckets.push(Vec::new());
            self.k = self.centroids.len();
            i
        } else {
            let mut best = 0usize;
            let mut best_score = f32::NEG_INFINITY;
            for (i, c) in self.centroids.iter().enumerate() {
                let s = cosine_similarity(vector, c);
                if s > best_score {
                    best_score = s;
                    best = i;
                }
            }
            // Accumulate raw sums and recompute mean — avoids FP drift from
            // repeated (mean * count + value) / new_count updates.
            let sum = &mut self.centroid_sums[best];
            for (i, &v) in vector.iter().enumerate() {
                sum[i] += v.to_f32();
            }
            self.counts[best] += 1;
            let count = self.counts[best] as f32;
            for (i, &s) in self.centroid_sums[best].iter().enumerate() {
                self.centroids[best][i] = f16::from_f32(s / count);
            }
            best
        };
        self.buckets[idx].push((id_hash, vec_page_id, vec_slot_index));
    }

    /// Target K = `max(initial_k, ceil(sqrt(total_vectors)))`.
    /// Existing bucket entries are preserved.
    pub fn rebuild_if_needed(&mut self, total_vectors: usize) {
        if total_vectors == 0 {
            return;
        }
        let target = self
            .initial_k
            .max((total_vectors as f64).sqrt().ceil() as usize)
            .max(1);
        if target == self.k {
            return;
        }
        let old_buckets = std::mem::take(&mut self.buckets);
        let mut new_buckets = vec![Vec::new(); target];
        for (i, b) in old_buckets.into_iter().enumerate() {
            let dst = if i < target { i } else { 0 };
            new_buckets[dst].extend(b);
        }
        self.buckets = new_buckets;
        if target < self.centroids.len() {
            self.centroids.truncate(target);
            self.centroid_sums.truncate(target);
            self.counts.truncate(target);
        } else {
            while self.centroids.len() < target {
                self.centroids.push(vec![f16::from_f32(0.0); self.dim]);
                self.centroid_sums.push(vec![0.0f32; self.dim]);
                self.counts.push(0);
            }
        }
        self.k = target;
    }

    fn serialize_centroids(&self) -> Vec<u8> {
        let mut bytes = Vec::with_capacity(12 + self.k * self.dim * 2);
        bytes.extend_from_slice(IVF_CLUSTER_MAGIC);
        bytes.push(IVF_VERSION);
        bytes.push(0); // flags
        bytes.extend_from_slice(&(self.dim as u16).to_le_bytes());
        bytes.extend_from_slice(&(self.k as u32).to_le_bytes());
        for c in &self.centroids {
            for v in c {
                bytes.extend_from_slice(&v.to_le_bytes());
            }
        }
        bytes
    }

    fn serialize_buckets(&self) -> Vec<u8> {
        let entry_bytes: usize = self.buckets.iter().map(|b| 4 + b.len() * 14).sum();
        let mut bytes = Vec::with_capacity(10 + entry_bytes);
        bytes.extend_from_slice(IVF_BUCKET_MAGIC);
        bytes.push(IVF_VERSION);
        bytes.push(0); // flags
        bytes.extend_from_slice(&(self.k as u32).to_le_bytes());
        for bucket in &self.buckets {
            bytes.extend_from_slice(&(bucket.len() as u32).to_le_bytes());
            for (id_hash, page_id, slot) in bucket {
                bytes.extend_from_slice(&id_hash.to_le_bytes());
                bytes.extend_from_slice(&page_id.to_le_bytes());
                bytes.extend_from_slice(&slot.to_le_bytes());
            }
        }
        bytes
    }
}

fn deserialize_centroids(bytes: &[u8]) -> MemHopResult<(usize, usize, Vec<Vec<f16>>)> {
    if bytes.len() < 12 || &bytes[0..4] != IVF_CLUSTER_MAGIC || bytes[4] != IVF_VERSION {
        return Err(MemHopError::Serialization(
            "Invalid IVF centroid data".into(),
        ));
    }
    let dim = u16::from_le_bytes([bytes[6], bytes[7]]) as usize;
    let k = u32::from_le_bytes([bytes[8], bytes[9], bytes[10], bytes[11]]) as usize;
    if bytes.len() < 12 + k * dim * 2 {
        return Err(MemHopError::Serialization(
            "Truncated IVF centroid data".into(),
        ));
    }
    let mut centroids = Vec::with_capacity(k);
    let mut offset = 12;
    for _ in 0..k {
        let mut c = Vec::with_capacity(dim);
        for _ in 0..dim {
            c.push(f16::from_le_bytes([bytes[offset], bytes[offset + 1]]));
            offset += 2;
        }
        centroids.push(c);
    }
    Ok((dim, k, centroids))
}

fn deserialize_buckets(bytes: &[u8]) -> MemHopResult<(usize, IvfBuckets)> {
    if bytes.len() < 10 || &bytes[0..4] != IVF_BUCKET_MAGIC || bytes[4] != IVF_VERSION {
        return Err(MemHopError::Serialization("Invalid IVF bucket data".into()));
    }
    let k = u32::from_le_bytes([bytes[6], bytes[7], bytes[8], bytes[9]]) as usize;
    let mut buckets = Vec::with_capacity(k);
    let mut offset = 10;
    for _ in 0..k {
        if offset + 4 > bytes.len() {
            return Err(MemHopError::Serialization(
                "Truncated IVF bucket header".into(),
            ));
        }
        let count = u32::from_le_bytes([
            bytes[offset],
            bytes[offset + 1],
            bytes[offset + 2],
            bytes[offset + 3],
        ]) as usize;
        offset += 4;
        let mut bucket = Vec::with_capacity(count);
        for _ in 0..count {
            if offset + 14 > bytes.len() {
                return Err(MemHopError::Serialization(
                    "Truncated IVF bucket entry".into(),
                ));
            }
            let id_hash =
                u64::from_le_bytes(bytes[offset..offset + 8].try_into().map_err(|_| {
                    MemHopError::Deserialization("truncated IVF bucket entry".into())
                })?);
            let page_id =
                u32::from_le_bytes(bytes[offset + 8..offset + 12].try_into().map_err(|_| {
                    MemHopError::Deserialization("truncated IVF bucket entry".into())
                })?);
            let slot = u16::from_le_bytes([bytes[offset + 12], bytes[offset + 13]]);
            bucket.push((id_hash, page_id, slot));
            offset += 14;
        }
        buckets.push(bucket);
    }
    Ok((k, buckets))
}

/// IVF approximate KNN: score centroids → probe `n_probes` buckets → exact cosine.
pub fn ivf_knn(
    ivf: &IVFIndex,
    data: &[u8],
    query_vector: &[f16],
    k_results: usize,
    n_probes: usize,
) -> MemHopResult<Vec<(u64, f32)>> {
    if ivf.centroids.is_empty() || ivf.buckets.is_empty() || query_vector.len() != ivf.dim {
        return Ok(vec![]);
    }
    let probes = n_probes.min(ivf.k);
    if probes == 0 {
        return Ok(vec![]);
    }
    let mut centroid_scores: Vec<(usize, f32)> = ivf
        .centroids
        .iter()
        .enumerate()
        .map(|(i, c)| (i, cosine_similarity(query_vector, c)))
        .collect();
    centroid_scores.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(Ordering::Equal));
    let selected: HashSet<usize> = centroid_scores.iter().take(probes).map(|x| x.0).collect();
    let mut seen = HashSet::<u64>::new();
    let mut candidates: Vec<(u64, f32)> = Vec::new();
    for &idx in &selected {
        for &(id_hash, vec_page_id, vec_slot_index) in &ivf.buckets[idx] {
            if !seen.insert(id_hash) {
                continue;
            }
            if let Ok(vector) = read_vector(data, vec_page_id, vec_slot_index, ivf.dim) {
                if vector.len() == ivf.dim {
                    candidates.push((id_hash, cosine_similarity(query_vector, &vector)));
                }
            }
        }
    }
    candidates.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(Ordering::Equal));
    candidates.truncate(k_results);
    Ok(candidates)
}

// ============================================================================
// IVF persistence helpers
// ============================================================================

fn read_page_header_from_mmap(mmap: &MmapMut, page_id: u32) -> MemHopResult<PageHeader> {
    let offset = page_id as usize * PAGE_SIZE;
    if offset + 32 > mmap.len() {
        return Err(MemHopError::PageNotFound(page_id));
    }
    let mut bytes = [0u8; 32];
    bytes.copy_from_slice(&mmap[offset..offset + 32]);
    PageHeader::from_bytes(&bytes)
}

fn read_page_data_from_mmap(mmap: &MmapMut, page_id: u32) -> MemHopResult<&[u8]> {
    let offset = page_id as usize * PAGE_SIZE;
    if offset + PAGE_SIZE > mmap.len() {
        return Err(MemHopError::PageNotFound(page_id));
    }
    Ok(&mmap[offset + 32..offset + PAGE_SIZE])
}

fn read_u16_from_reserved(r: &[u8; 3988], o: usize) -> u16 {
    u16::from_le_bytes([r[o], r[o + 1]])
}
fn read_u32_from_reserved(r: &[u8; 3988], o: usize) -> u32 {
    u32::from_le_bytes([r[o], r[o + 1], r[o + 2], r[o + 3]])
}
fn write_u16_to_reserved(r: &mut [u8; 3988], o: usize, v: u16) {
    r[o..o + 2].copy_from_slice(&v.to_le_bytes());
}
fn write_u32_to_reserved(r: &mut [u8; 3988], o: usize, v: u32) {
    r[o..o + 4].copy_from_slice(&v.to_le_bytes());
}

fn allocate_and_write_chain(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    page_type: PageType,
    data: &[u8],
    file: &mut File,
) -> MemHopResult<u32> {
    let cap = PAGE_SIZE - 32;
    let n = if data.is_empty() {
        1
    } else {
        data.len().div_ceil(cap)
    };
    let mut pages = Vec::with_capacity(n);
    for _ in 0..n {
        pages.push(allocate_page(
            mmap,
            header,
            page_type,
            0,
            EMPTY_FREE_LIST,
            file,
        )?);
    }
    for (i, &pid) in pages.iter().enumerate() {
        let start = i * cap;
        let chunk = if start >= data.len() {
            &[] as &[u8]
        } else {
            &data[start..(start + cap).min(data.len())]
        };
        write_page_data(mmap, pid, chunk)?;
        if i + 1 < pages.len() {
            let mut ph = read_page_header_from_mmap(mmap, pid)?;
            ph.next_page = pages[i + 1];
            write_page_header(mmap, pid, &ph)?;
        }
    }
    Ok(pages[0])
}

fn read_chain(mmap: &MmapMut, first_page_id: u32) -> MemHopResult<Vec<u8>> {
    let mut result = Vec::new();
    let mut current = first_page_id;
    // `visited` guards against cyclic page chains.
    let mut visited = HashSet::new();
    while current != EMPTY_FREE_LIST && current != 0 {
        if !visited.insert(current) {
            break;
        }
        result.extend_from_slice(read_page_data_from_mmap(mmap, current)?);
        current = read_page_header_from_mmap(mmap, current)?.next_page;
    }
    Ok(result)
}

fn free_page_chain(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    start_page: u32,
) -> MemHopResult<()> {
    let mut current = start_page;
    let mut visited = HashSet::new();
    while current != EMPTY_FREE_LIST && current != 0 {
        if !visited.insert(current) {
            break;
        }
        let next = read_page_header_from_mmap(mmap, current)
            .map(|h| h.next_page)
            .unwrap_or(EMPTY_FREE_LIST);
        crate::file::free_list::free_page(mmap, header, current)?;
        current = next;
    }
    Ok(())
}

/// Persist centroids + buckets, free old chains, store roots in `header.reserved`.
pub fn write_ivf_index(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    index: &IVFIndex,
    file: &mut File,
) -> MemHopResult<()> {
    let old_cr = read_u32_from_reserved(&header.reserved, IVF_CLUSTER_ROOT_OFFSET);
    let old_br = read_u32_from_reserved(&header.reserved, IVF_BUCKET_ROOT_OFFSET);
    // Free old chains first so new chains can reuse the pages safely.
    if old_cr != 0 {
        free_page_chain(mmap, header, old_cr)?;
    }
    if old_br != 0 {
        free_page_chain(mmap, header, old_br)?;
    }
    let cluster_root = if index.centroids.is_empty() {
        0
    } else {
        allocate_and_write_chain(
            mmap,
            header,
            PageType::IVFCluster,
            &index.serialize_centroids(),
            file,
        )?
    };
    let bucket_root = if index.buckets.is_empty() {
        0
    } else {
        allocate_and_write_chain(
            mmap,
            header,
            PageType::IVFBucket,
            &index.serialize_buckets(),
            file,
        )?
    };
    write_u32_to_reserved(&mut header.reserved, IVF_CLUSTER_ROOT_OFFSET, cluster_root);
    write_u32_to_reserved(&mut header.reserved, IVF_BUCKET_ROOT_OFFSET, bucket_root);
    write_u16_to_reserved(&mut header.reserved, IVF_DIM_OFFSET, index.dim as u16);
    write_u32_to_reserved(&mut header.reserved, IVF_K_OFFSET, index.k as u32);
    Ok(())
}

/// Load IVF from disk. Returns `None` if no root pages are recorded.
pub fn read_ivf_index(mmap: &MmapMut, header: &FileHeader) -> MemHopResult<Option<IVFIndex>> {
    let cr = read_u32_from_reserved(&header.reserved, IVF_CLUSTER_ROOT_OFFSET);
    let br = read_u32_from_reserved(&header.reserved, IVF_BUCKET_ROOT_OFFSET);
    if cr == 0 || br == 0 {
        return Ok(None);
    }
    let dim = read_u16_from_reserved(&header.reserved, IVF_DIM_OFFSET) as usize;
    let k = read_u32_from_reserved(&header.reserved, IVF_K_OFFSET) as usize;
    let (rd, rk, centroids) = deserialize_centroids(&read_chain(mmap, cr)?)?;
    let (rk2, buckets) = deserialize_buckets(&read_chain(mmap, br)?)?;
    if rd != dim || rk != k || rk != rk2 {
        return Err(MemHopError::Serialization(
            "IVF persisted metadata mismatch".into(),
        ));
    }
    let mut centroid_sums = Vec::with_capacity(k);
    let mut counts = Vec::with_capacity(k);
    for (i, c) in centroids.iter().enumerate() {
        let count = buckets.get(i).map(|b| b.len()).unwrap_or(0).max(1);
        counts.push(count);
        centroid_sums.push(c.iter().map(|x| x.to_f32() * count as f32).collect());
    }
    Ok(Some(IVFIndex {
        centroids,
        buckets,
        dim,
        initial_k: k,
        k,
        centroid_sums,
        counts,
    }))
}

#[cfg(test)]
mod tests {
    use super::*;
    use memmap2::Mmap;

    #[test]
    fn test_vector_page_layout() {
        let page = VectorPage::new(768);
        assert_eq!(page.slot_size(), 16 + 768 * 2);
        assert_eq!(page.vectors_per_page(), (4096 - 32) / 1552);
    }

    #[test]
    fn test_cosine_similarity_identical() {
        let a = vec![f16::from_f32(1.0), f16::from_f32(0.0), f16::from_f32(0.0)];
        let b = a.clone();
        assert!((cosine_similarity(&a, &b) - 1.0).abs() < 1e-5);
    }

    #[test]
    fn test_cosine_similarity_orthogonal() {
        let a = vec![f16::from_f32(1.0), f16::from_f32(0.0), f16::from_f32(0.0)];
        let b = vec![f16::from_f32(0.0), f16::from_f32(1.0), f16::from_f32(0.0)];
        assert!(cosine_similarity(&a, &b).abs() < 1e-5);
    }

    #[test]
    fn test_cosine_similarity_opposite() {
        let a = vec![f16::from_f32(1.0), f16::from_f32(0.0), f16::from_f32(0.0)];
        let b = vec![f16::from_f32(-1.0), f16::from_f32(0.0), f16::from_f32(0.0)];
        assert!((cosine_similarity(&a, &b) + 1.0).abs() < 1e-5);
    }

    #[test]
    fn test_cosine_similarity_partial() {
        let a = vec![f16::from_f32(1.0), f16::from_f32(1.0), f16::from_f32(0.0)];
        let b = vec![f16::from_f32(1.0), f16::from_f32(0.0), f16::from_f32(0.0)];
        let sim = cosine_similarity(&a, &b);
        assert!(
            (sim - std::f32::consts::FRAC_1_SQRT_2).abs() < 0.01,
            "got {}",
            sim
        );
    }

    #[test]
    fn test_cosine_similarity_zero_vector() {
        let a = vec![f16::from_f32(0.0); 3];
        let b = vec![f16::from_f32(1.0), f16::from_f32(0.0), f16::from_f32(0.0)];
        assert_eq!(cosine_similarity(&a, &b), 0.0);
    }

    #[test]
    fn test_vector_read_write() {
        use std::io::Write;
        use tempfile::NamedTempFile;
        let dim = 4;
        let file = NamedTempFile::new().unwrap();
        let mut f = std::fs::File::create(file.path()).unwrap();
        f.write_all(&vec![0u8; PAGE_SIZE]).unwrap();
        drop(f);
        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(file.path())
            .unwrap();
        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let original = vec![
            f16::from_f32(1.0),
            f16::from_f32(2.0),
            f16::from_f32(3.0),
            f16::from_f32(4.0),
        ];
        write_vector(&mut mmap, 0, 0, 12345, &original, dim).unwrap();
        let mmap = unsafe { Mmap::map(&file).unwrap() };
        let read_back = read_vector(&mmap, 0, 0, dim).unwrap();
        for (o, r) in original.iter().zip(read_back.iter()) {
            assert!((o.to_f32() - r.to_f32()).abs() < 1e-5);
        }
    }

    #[test]
    fn test_brute_force_knn_empty() {
        let data: &[u8] = &vec![0u8; PAGE_SIZE];
        let btree = BTreeIndex::new();
        let query = vec![f16::from_f32(1.0), f16::from_f32(0.0), f16::from_f32(0.0)];
        assert!(brute_force_knn(data, &query, &btree, 3, 100, 10)
            .unwrap()
            .is_empty());
    }

    #[test]
    fn test_cosine_similarity_neon_aligned() {
        let a: Vec<f16> = (1..=8).map(|x| f16::from_f32(x as f32)).collect();
        let b: Vec<f16> = (2..=9).map(|x| f16::from_f32(x as f32)).collect();
        let sim = cosine_similarity(&a, &b);
        let expected = cosine_similarity_fallback(&a, &b);
        assert!(
            (sim - expected).abs() < 1e-4,
            "SIMD {} vs fallback {}",
            sim,
            expected
        );
    }

    #[cfg(target_arch = "aarch64")]
    #[test]
    fn test_cosine_similarity_neon_path_directly() {
        if !is_aarch64_feature_detected!("neon") {
            return;
        }
        let a: Vec<f16> = (1..=5).map(|x| f16::from_f32(x as f32 * 0.1)).collect();
        let b: Vec<f16> = (1..=5)
            .rev()
            .map(|x| f16::from_f32(x as f32 * 0.1))
            .collect();
        let neon_sim = unsafe { cosine_similarity_neon(&a, &b) };
        let fallback_sim = cosine_similarity_fallback(&a, &b);
        assert!((neon_sim - fallback_sim).abs() < 1e-4);
    }

    #[test]
    fn test_ivf_index_add_and_assign() {
        let mut ivf = IVFIndex::new(4, 2);
        let v1 = vec![
            f16::from_f32(1.0),
            f16::from_f32(0.0),
            f16::from_f32(0.0),
            f16::from_f32(0.0),
        ];
        let v2 = vec![
            f16::from_f32(0.0),
            f16::from_f32(1.0),
            f16::from_f32(0.0),
            f16::from_f32(0.0),
        ];
        let v3 = vec![
            f16::from_f32(0.9),
            f16::from_f32(0.1),
            f16::from_f32(0.0),
            f16::from_f32(0.0),
        ];
        ivf.add_vector(1, &v1, 10, 0);
        ivf.add_vector(2, &v2, 11, 0);
        ivf.add_vector(3, &v3, 12, 0);
        assert_eq!(ivf.k, 2);
        assert_eq!(ivf.len(), 3);
        let b0 = ivf.buckets[0].len();
        let b1 = ivf.buckets[1].len();
        assert!((b0 == 2 && b1 == 1) || (b0 == 1 && b1 == 2));
    }

    #[test]
    fn test_ivf_knn_matches_brute_force() {
        use crate::file::page::encode_page_ref;
        use crate::layers::context_node::ContextNode;
        use std::io::Write;
        use tempfile::NamedTempFile;
        let dim = 4usize;
        let vectors: Vec<(u64, Vec<f16>)> = vec![
            (
                1,
                vec![
                    f16::from_f32(1.0),
                    f16::from_f32(0.0),
                    f16::from_f32(0.0),
                    f16::from_f32(0.0),
                ],
            ),
            (
                2,
                vec![
                    f16::from_f32(0.0),
                    f16::from_f32(1.0),
                    f16::from_f32(0.0),
                    f16::from_f32(0.0),
                ],
            ),
            (
                3,
                vec![
                    f16::from_f32(0.0),
                    f16::from_f32(0.0),
                    f16::from_f32(1.0),
                    f16::from_f32(0.0),
                ],
            ),
            (
                4,
                vec![
                    f16::from_f32(0.9),
                    f16::from_f32(0.1),
                    f16::from_f32(0.0),
                    f16::from_f32(0.0),
                ],
            ),
            (
                5,
                vec![
                    f16::from_f32(0.1),
                    f16::from_f32(0.9),
                    f16::from_f32(0.0),
                    f16::from_f32(0.0),
                ],
            ),
        ];
        let file = NamedTempFile::new().unwrap();
        let mut f = std::fs::File::create(file.path()).unwrap();
        f.write_all(&vec![0u8; PAGE_SIZE * 20]).unwrap();
        drop(f);
        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(file.path())
            .unwrap();
        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut btree = BTreeIndex::new();
        let mut ivf = IVFIndex::new(dim, 2);
        for (i, (id, vec)) in vectors.iter().enumerate() {
            let node_page = (2 + i) as u32;
            let vec_page = (10 + i) as u32;
            let node = ContextNode {
                id_hash: *id,
                context_id: *id,
                vector_page_ref: encode_page_ref(vec_page, 0),
                importance: 1.0,
                valence: 0.0,
                arousal: 0.0,
                created_at: 0,
                updated_at: 0,
                version: 1,
                edge_ptrs: vec![],
            };
            let nb = node.serialize().unwrap();
            mmap[node_page as usize * PAGE_SIZE + 32
                ..node_page as usize * PAGE_SIZE + 32 + nb.len()]
                .copy_from_slice(&nb);
            write_vector(&mut mmap, vec_page, 0, *id, vec, dim).unwrap();
            btree.insert(*id, encode_page_ref(node_page, 0));
            ivf.add_vector(*id, vec, vec_page, 0);
        }
        let data: &[u8] = &mmap[..];
        let query = vec![
            f16::from_f32(1.0),
            f16::from_f32(0.0),
            f16::from_f32(0.0),
            f16::from_f32(0.0),
        ];
        let brute = brute_force_knn(data, &query, &btree, dim, 20, 3).unwrap();
        let ivf_results = ivf_knn(&ivf, data, &query, 3, ivf.k).unwrap();
        assert_eq!(brute.len(), ivf_results.len());
        for ((bid, bs), (iid, is)) in brute.iter().zip(ivf_results.iter()) {
            assert_eq!(bid, iid);
            assert!((bs - is).abs() < 1e-4, "brute {} ivf {}", bs, is);
        }
    }

    #[test]
    fn test_ivf_serialization_roundtrip() {
        use crate::file::free_list::{free_page, init_free_list};
        use std::io::Write;
        use tempfile::NamedTempFile;
        let dim = 8usize;
        let mut ivf = IVFIndex::new(dim, 2);
        ivf.add_vector(101, &vec![f16::from_f32(1.0); dim], 7, 0);
        ivf.add_vector(102, &vec![f16::from_f32(0.0); dim], 8, 1);
        ivf.add_vector(103, &vec![f16::from_f32(0.5); dim], 9, 0);
        let file = NamedTempFile::new().unwrap();
        let mut f = std::fs::File::create(file.path()).unwrap();
        f.write_all(&vec![0u8; PAGE_SIZE * 20]).unwrap();
        drop(f);
        let mut file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(file.path())
            .unwrap();
        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = FileHeader::new(dim as u16);
        init_free_list(&mut header).unwrap();
        for pid in (2..20).rev() {
            free_page(&mut mmap, &mut header, pid).unwrap();
        }
        write_ivf_index(&mut mmap, &mut header, &ivf, &mut file).unwrap();
        let r = read_ivf_index(&mmap, &header)
            .unwrap()
            .expect("IVF present");
        assert_eq!(r.dim, ivf.dim);
        assert_eq!(r.k, ivf.k);
        assert_eq!(r.buckets, ivf.buckets);
        let old_cr = read_u32_from_reserved(&header.reserved, IVF_CLUSTER_ROOT_OFFSET);
        let old_br = read_u32_from_reserved(&header.reserved, IVF_BUCKET_ROOT_OFFSET);
        write_ivf_index(&mut mmap, &mut header, &ivf, &mut file).unwrap();
        let new_cr = read_u32_from_reserved(&header.reserved, IVF_CLUSTER_ROOT_OFFSET);
        let new_br = read_u32_from_reserved(&header.reserved, IVF_BUCKET_ROOT_OFFSET);
        assert!(new_cr != 0 && new_br != 0);
        assert!(new_cr != old_cr || old_cr == 0);
        assert!(new_br != old_br || old_br == 0);
    }

    #[test]
    fn test_ivf_rebuild_if_needed() {
        let mut ivf = IVFIndex::new(4, 2);
        let v = vec![
            f16::from_f32(1.0),
            f16::from_f32(0.0),
            f16::from_f32(0.0),
            f16::from_f32(0.0),
        ];
        for id in 1..=16u64 {
            ivf.add_vector(id, &v, id as u32, 0);
        }
        // sqrt(16) = 4, initial_k = 2 -> target 4
        ivf.rebuild_if_needed(ivf.len());
        assert_eq!(ivf.k, 4);
        assert_eq!(ivf.len(), 16);
    }

    #[test]
    fn test_ivf_accumulation_precision() {
        let dim = 8usize;
        let mut ivf = IVFIndex::new(dim, 1);
        let n = 1000usize;
        let mut true_sum = vec![0.0f32; dim];
        for i in 0..n {
            let mut v = vec![f16::from_f32(1.0); dim];
            v[0] = f16::from_f32(i as f32);
            for (j, &val) in v.iter().enumerate() {
                true_sum[j] += val.to_f32();
            }
            ivf.add_vector(i as u64, &v, i as u32, 0);
        }
        let count = n as f32;
        let expected_mean: Vec<f32> = true_sum.iter().map(|&s| s / count).collect();
        assert_eq!(ivf.counts[0], n);
        for (i, (a, e)) in ivf.centroids[0]
            .iter()
            .zip(expected_mean.iter())
            .enumerate()
        {
            assert!(
                (a.to_f32() - e).abs() < 1e-2,
                "dim {} actual {} expected {}",
                i,
                a.to_f32(),
                e
            );
        }
    }
}
