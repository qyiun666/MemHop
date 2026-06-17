// Vector matrix module (SIMD)
use crate::index::btree::BTreeIndex;
use crate::slot::context_node::ContextNode;
use crate::util::PAGE_SIZE;
use crate::MemHopError;
use half::f16;
use memmap2::MmapMut;
use std::cmp::Ordering;
use std::io::Result;

/// Vector page structure for storing embeddings
pub struct VectorPage {
    pub dim: usize, // 向量维度
}

impl VectorPage {
    /// Create a new VectorPage with specified dimension
    pub fn new(dim: usize) -> Self {
        Self { dim }
    }

    /// Calculate slot size in bytes: id_hash(u64, 8) + last_access(i64, 8) + f16[dim]
    pub fn slot_size(&self) -> usize {
        16 + self.dim * 2
    }

    /// Calculate number of vectors per page
    /// Formula: (PAGE_SIZE - 32) / slot_size
    /// 32 bytes reserved for page metadata
    pub fn vectors_per_page(&self) -> usize {
        (PAGE_SIZE - 32) / self.slot_size()
    }

    /// Calculate byte offset for a specific slot
    /// Offset = 32 (header) + slot_index * slot_size
    pub fn slot_offset(&self, slot_index: u16) -> usize {
        32 + (slot_index as usize) * self.slot_size()
    }
}

/// Calculate cosine similarity between two f16 vectors using AVX2 (x86_64 only)
#[cfg(target_arch = "x86_64")]
#[target_feature(enable = "avx2")]
unsafe fn cosine_similarity_avx2(a: &[f16], b: &[f16]) -> f32 {
    use std::arch::x86_64::*;

    let len = a.len();
    assert_eq!(len, b.len(), "Vector lengths must match");

    // Convert f16 to f32 for computation
    let mut dot_product = 0.0_f32;
    let mut norm_a = 0.0_f32;
    let mut norm_b = 0.0_f32;

    // Process 8 elements at a time using AVX2 (256-bit registers)
    let mut i = 0;
    while i + 8 <= len {
        // Load 8 f16 values and convert to f32
        let a_vals: [f32; 8] = std::array::from_fn(|j| a[i + j].to_f32());
        let b_vals: [f32; 8] = std::array::from_fn(|j| b[i + j].to_f32());

        let va = _mm256_loadu_ps(a_vals.as_ptr());
        let vb = _mm256_loadu_ps(b_vals.as_ptr());

        // Compute dot product contribution
        let prod = _mm256_mul_ps(va, vb);
        dot_product += _mm256_reduce_add_ps(prod);

        // Compute norms
        let a_sq = _mm256_mul_ps(va, va);
        let b_sq = _mm256_mul_ps(vb, vb);
        norm_a += _mm256_reduce_add_ps(a_sq);
        norm_b += _mm256_reduce_add_ps(b_sq);

        i += 8;
    }

    // Handle remaining elements
    while i < len {
        let a_val = a[i].to_f32();
        let b_val = b[i].to_f32();
        dot_product += a_val * b_val;
        norm_a += a_val * a_val;
        norm_b += b_val * b_val;
        i += 1;
    }

    // Calculate cosine similarity
    let denom = (norm_a * norm_b).sqrt();
    if denom < 1e-10 {
        0.0
    } else {
        dot_product / denom
    }
}

/// Helper function to reduce __m256 to scalar sum
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

/// Scalar fallback implementation of cosine similarity
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

/// Public interface for cosine similarity with automatic SIMD detection
pub fn cosine_similarity(a: &[f16], b: &[f16]) -> f32 {
    #[cfg(target_arch = "x86_64")]
    {
        if is_x86_feature_detected!("avx2") {
            return unsafe { cosine_similarity_avx2(a, b) };
        }
    }
    cosine_similarity_fallback(a, b)
}

/// Brute-force KNN search across all vector pages
/// Returns top-k (id_hash, cosine_score) pairs
pub fn brute_force_knn(
    data: &[u8],
    query_vector: &[f16],
    btree: &BTreeIndex,
    vector_dim: usize,
    _page_count: u32,
    k: usize,
) -> std::result::Result<Vec<(u64, f32)>, MemHopError> {
    let mut candidates: Vec<(u64, f32)> = Vec::new();

    // Iterate through all entries in btree
    for (&id_hash, &page_ref) in btree.iter() {
        // Extract node page reference
        let node_page_id = (page_ref >> 16) as u32;
        let node_offset = (node_page_id as usize) * PAGE_SIZE + 32;

        // Check bounds
        if node_offset >= data.len() {
            continue;
        }

        // Deserialize ContextNode to get vector_page_ref
        if let Ok(node) = ContextNode::deserialize(&data[node_offset..]) {
            // Skip if no vector
            if node.vector_page_ref == 0 {
                continue;
            }

            // Extract vector page reference
            let vec_page_id = (node.vector_page_ref >> 16) as u32;
            let vec_slot_index = (node.vector_page_ref & 0xFFFF) as u16;

            // Read vector from mmap
            if let Ok(vector) = read_vector(data, vec_page_id, vec_slot_index, vector_dim) {
                // Calculate cosine similarity
                let score = cosine_similarity(query_vector, &vector);
                candidates.push((id_hash, score));
            }
        }
    }

    // Sort by score descending and return top-k
    candidates.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(Ordering::Equal));
    candidates.truncate(k);

    Ok(candidates)
}

/// Write a vector to a specific slot in the memory-mapped file
/// Layout per slot: [id_hash: u64 (8 bytes)] [last_access: i64 (8 bytes)] [vector: f16[dim]]
pub fn write_vector(
    mmap: &mut MmapMut,
    page_id: u32,
    slot_index: u16,
    id_hash: u64,
    vector: &[f16],
    dim: usize,
) -> Result<()> {
    let page = VectorPage::new(dim);
    let offset = page_id as usize * PAGE_SIZE + page.slot_offset(slot_index);
    let slot_size = page.slot_size();

    // Ensure we don't write beyond the mapping
    if offset + slot_size > mmap.len() {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidInput,
            "Write would exceed mmap bounds",
        ));
    }

    // Write id_hash (8 bytes, little-endian)
    mmap[offset..offset + 8].copy_from_slice(&id_hash.to_le_bytes());

    // Write last_access timestamp (8 bytes, little-endian)
    // Using current timestamp or 0 for now
    let last_access = 0i64;
    mmap[offset + 8..offset + 16].copy_from_slice(&last_access.to_le_bytes());

    // Write vector data (dim * 2 bytes)
    let vector_start = offset + 16;
    for (i, &val) in vector.iter().enumerate() {
        let bytes = val.to_le_bytes();
        mmap[vector_start + i * 2..vector_start + i * 2 + 2].copy_from_slice(&bytes);
    }

    Ok(())
}

/// Read a vector from a specific slot in the memory-mapped data
pub fn read_vector(data: &[u8], page_id: u32, slot_index: u16, dim: usize) -> Result<Vec<f16>> {
    let page = VectorPage::new(dim);
    let offset = page_id as usize * PAGE_SIZE + page.slot_offset(slot_index);
    let slot_size = page.slot_size();

    // Ensure we don't read beyond the data
    if offset + slot_size > data.len() {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidInput,
            "Read would exceed data bounds",
        ));
    }

    // Skip id_hash (8 bytes) and last_access (8 bytes)
    let vector_start = offset + 16;
    let mut vector = Vec::with_capacity(dim);

    for i in 0..dim {
        let bytes: [u8; 2] = [data[vector_start + i * 2], data[vector_start + i * 2 + 1]];
        vector.push(f16::from_le_bytes(bytes));
    }

    Ok(vector)
}

#[cfg(test)]
mod tests {
    use super::*;
    use memmap2::Mmap;

    #[test]
    fn test_vector_page_layout() {
        let page = VectorPage::new(768);
        assert_eq!(page.slot_size(), 16 + 768 * 2); // 1552 bytes
        assert_eq!(page.vectors_per_page(), (4096 - 32) / 1552); // 2 vectors per page
    }

    #[test]
    fn test_cosine_similarity_identical() {
        let a = vec![f16::from_f32(1.0), f16::from_f32(0.0), f16::from_f32(0.0)];
        let b = vec![f16::from_f32(1.0), f16::from_f32(0.0), f16::from_f32(0.0)];
        let sim = cosine_similarity(&a, &b);
        assert!(
            (sim - 1.0).abs() < 1e-5,
            "Identical vectors should have similarity 1.0, got {}",
            sim
        );
    }

    #[test]
    fn test_cosine_similarity_orthogonal() {
        let a = vec![f16::from_f32(1.0), f16::from_f32(0.0), f16::from_f32(0.0)];
        let b = vec![f16::from_f32(0.0), f16::from_f32(1.0), f16::from_f32(0.0)];
        let sim = cosine_similarity(&a, &b);
        assert!(
            sim.abs() < 1e-5,
            "Orthogonal vectors should have similarity 0.0, got {}",
            sim
        );
    }

    #[test]
    fn test_cosine_similarity_opposite() {
        let a = vec![f16::from_f32(1.0), f16::from_f32(0.0), f16::from_f32(0.0)];
        let b = vec![f16::from_f32(-1.0), f16::from_f32(0.0), f16::from_f32(0.0)];
        let sim = cosine_similarity(&a, &b);
        assert!(
            (sim + 1.0).abs() < 1e-5,
            "Opposite vectors should have similarity -1.0, got {}",
            sim
        );
    }

    #[test]
    fn test_cosine_similarity_partial() {
        let a = vec![f16::from_f32(1.0), f16::from_f32(1.0), f16::from_f32(0.0)];
        let b = vec![f16::from_f32(1.0), f16::from_f32(0.0), f16::from_f32(0.0)];
        let sim = cosine_similarity(&a, &b);
        // cos(45°) = 1/sqrt(2) ≈ 0.7071
        assert!(
            (sim - std::f32::consts::FRAC_1_SQRT_2).abs() < 0.01,
            "Expected ~0.7071, got {}",
            sim
        );
    }

    #[test]
    fn test_cosine_similarity_zero_vector() {
        let a = vec![f16::from_f32(0.0), f16::from_f32(0.0), f16::from_f32(0.0)];
        let b = vec![f16::from_f32(1.0), f16::from_f32(0.0), f16::from_f32(0.0)];
        let sim = cosine_similarity(&a, &b);
        assert_eq!(sim, 0.0, "Zero vector should have similarity 0.0");
    }

    #[test]
    fn test_vector_read_write() {
        use std::io::Write;
        use tempfile::NamedTempFile;

        let dim = 4;
        let file = NamedTempFile::new().unwrap();
        let path = file.path();

        // Create a file with enough space for one page
        let mut f = std::fs::File::create(path).unwrap();
        f.write_all(&vec![0u8; PAGE_SIZE]).unwrap();
        drop(f);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };

        // Test vector
        let original = vec![
            f16::from_f32(1.0),
            f16::from_f32(2.0),
            f16::from_f32(3.0),
            f16::from_f32(4.0),
        ];

        // Write vector
        write_vector(&mut mmap, 0, 0, 12345, &original, dim).unwrap();

        // Make the mmap immutable for reading
        let mmap = unsafe { Mmap::map(&file).unwrap() };

        // Read vector back from raw byte slice
        let read_back = read_vector(&mmap, 0, 0, dim).unwrap();

        assert_eq!(original.len(), read_back.len());
        for (orig, read) in original.iter().zip(read_back.iter()) {
            assert!((orig.to_f32() - read.to_f32()).abs() < 1e-5);
        }
    }

    #[test]
    fn test_brute_force_knn_empty() {
        let data: &[u8] = &vec![0u8; PAGE_SIZE];
        let btree = BTreeIndex::new();
        let query = vec![f16::from_f32(1.0), f16::from_f32(0.0), f16::from_f32(0.0)];

        let results = brute_force_knn(data, &query, &btree, 3, 100, 10).unwrap();
        assert!(results.is_empty());
    }
}
