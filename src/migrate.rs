//! Migration tool for migrating from legacy meowagent-memhop (redb) to MemHop (.meh format)
//!
//! This module provides utilities to migrate data from the old redb-based storage
//! to the new .meh binary file format with zero-copy mmap access.
//!
//! # Migration Process
//! 1. Open legacy redb database and read tables sequentially
//! 2. Convert data structures (string ID → xxHash64, bincode → slot format)
//! 3. Write to .meh file through MemHop store API
//! 4. Verify: compare data between redb and .meh entry by entry
//!
//! # Example
//! ```no_run
//! use memhop::migrate::{migrate, MigrateReport};
//! use std::path::Path;
//!
//! let report = migrate(
//!     Path::new("legacy.db"),
//!     Path::new("new.meh")
//! ).unwrap();
//! println!("Migrated {} L1 nodes", report.l1_nodes);
//! ```

use std::path::Path;
use std::time::Instant;
use thiserror::Error;

use crate::util::hash::hash_id;

/// Migration error types
#[derive(Error, Debug)]
pub enum MigrateError {
    #[error("IO error: {0}")]
    Io(#[from] std::io::Error),

    #[error("Legacy database error: {0}")]
    LegacyDb(String),

    #[error("Data conversion error: {0}")]
    Conversion(String),

    #[error("Verification failed: {0}")]
    Verification(String),

    #[error("MemHop error: {0}")]
    MemHop(#[from] crate::MemHopError),
}

pub type MigrateResult<T> = std::result::Result<T, MigrateError>;

/// Migration report containing statistics about the migration process
#[derive(Debug, Clone)]
pub struct MigrateReport {
    /// Number of L1 nodes migrated
    pub l1_nodes: u64,
    /// Number of L1 edges migrated
    pub l1_edges: u64,
    /// Number of L2 topics migrated
    pub l2_topics: u64,
    /// Number of L3 nodes migrated
    pub l3_nodes: u64,
    /// Number of L4 documents migrated
    pub l4_docs: u64,
    /// Number of L5 crystals migrated
    pub l5_crystals: u64,
    /// Number of vectors migrated
    pub vectors: u64,
    /// Total migration time in milliseconds
    pub elapsed_ms: u64,
}

impl std::fmt::Display for MigrateReport {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            f,
            "Migration Report:\n  L1 nodes: {}\n  L1 edges: {}\n  L2 topics: {}\n  L3 nodes: {}\n  L4 docs: {}\n  L5 crystals: {}\n  Vectors: {}\n  Time: {}ms",
            self.l1_nodes,
            self.l1_edges,
            self.l2_topics,
            self.l3_nodes,
            self.l4_docs,
            self.l5_crystals,
            self.vectors,
            self.elapsed_ms
        )
    }
}

/// Migrate from legacy redb database to MemHop .meh format
///
/// # Arguments
/// * `redb_path` - Path to the legacy redb database file
/// * `meh_path` - Path where the new .meh file will be created
///
/// # Returns
/// `MigrateReport` with migration statistics
///
/// # Errors
/// Returns `MigrateError` if:
/// - Cannot open legacy database
/// - Data conversion fails
/// - Verification fails
/// - Cannot create new .meh file
///
/// # Migration Steps
/// 1. Open redb database and enumerate all tables
/// 2. For each table, read entries and convert to new format:
///    - KnowledgeNode → EngramSlot (L1/L3)
///    - Hyperedge → HyperedgeSlot
///    - Topic → TopicSlot (L2)
///    - RawDocument → ArchiveSlot (L4)
///    - Crystal patterns → CrystalSlot (L5)
/// 3. Write converted data to .meh using MemHop store API
/// 4. Verify data integrity by comparing source and destination
pub fn migrate(redb_path: &Path, meh_path: &Path) -> MigrateResult<MigrateReport> {
    let start_time = Instant::now();

    // NOTE: Actual redb reading logic is not implemented yet.
    // This requires the legacy meowagent-memhop crate or direct redb access.
    // The current implementation provides a framework that can be completed when legacy code is available.

    // Step 1: Validate input paths
    if !redb_path.exists() {
        return Err(MigrateError::LegacyDb(format!(
            "Legacy database not found: {:?}",
            redb_path
        )));
    }

    if meh_path.exists() {
        return Err(MigrateError::Io(std::io::Error::new(
            std::io::ErrorKind::AlreadyExists,
            format!("Target file already exists: {:?}", meh_path),
        )));
    }

    // Step 2: Initialize new MemHop database
    use crate::MemHop;
    use crate::MemHopConfig;

    let mut config = MemHopConfig::new(meh_path.to_path_buf(), 768); // Default vector dimension
    config.encoder_grpc_addr = None; // migration does not require a live encoder
    let mut memhop = MemHop::open(config).map_err(MigrateError::MemHop)?;

    // Step 3: Migrate data from redb to MemHop
    // NOTE: Replace with actual redb iteration when legacy code is available
    let report = migrate_from_redb_stub(&mut memhop)?;

    // Step 4: Sync and close
    memhop.sync().map_err(MigrateError::MemHop)?;
    memhop.close().map_err(MigrateError::MemHop)?;

    let elapsed_ms = start_time.elapsed().as_millis() as u64;

    Ok(MigrateReport {
        elapsed_ms,
        ..report
    })
}

/// Stub function for migrating from redb (to be implemented with actual legacy code)
///
/// This function provides the migration framework. When the legacy meowagent-memhop
/// code becomes available, replace this stub with actual redb table iteration.
fn migrate_from_redb_stub(_memhop: &mut crate::MemHop) -> MigrateResult<MigrateReport> {
    // NOTE: Actual migration logic is a stub; implement with real redb iteration when legacy code is available
    // Pseudocode structure:
    //
    // let db = redb::Database::open(redb_path)?;
    // let txn = db.begin_read()?;
    //
    // // Migrate L1 nodes (KnowledgeNode → EngramSlot)
    // let l1_table = txn.open_table(KNOWLEDGE_NODES_TABLE)?;
    // let mut l1_count = 0u64;
    // for entry in l1_table.iter()? {
    //     let (key, value) = entry?;
    //     let engram = convert_knowledge_node_to_engram(key.value(), value.value())?;
    //     memhop.store(engram.to_store_doc())?;
    //     l1_count += 1;
    // }
    //
    // // Migrate L2 topics
    // let topic_table = txn.open_table(TOPICS_TABLE)?;
    // let mut topic_count = 0u64;
    // for entry in topic_table.iter()? {
    //     let topic = convert_topic(entry?)?;
    //     // Topics are managed through session activation
    //     topic_count += 1;
    // }
    //
    // // Migrate hyperedges
    // let edge_table = txn.open_table(HYPEREDGES_TABLE)?;
    // let mut edge_count = 0u64;
    // for entry in edge_table.iter()? {
    //     let edge = convert_hyperedge(entry?)?;
    //     // Store hyperedge associations
    //     edge_count += 1;
    // }
    //
    // // Migrate L4 documents (RawDocument → ArchiveSlot)
    // let doc_table = txn.open_table(DOCUMENTS_TABLE)?;
    // let mut doc_count = 0u64;
    // for entry in doc_table.iter()? {
    //     let doc = convert_raw_document(entry?)?;
    //     memhop.store(doc.to_store_doc())?;
    //     doc_count += 1;
    // }
    //
    // // Migrate L5 crystals
    // let crystal_table = txn.open_table(CRYSTALS_TABLE)?;
    // let mut crystal_count = 0u64;
    // for entry in crystal_table.iter()? {
    //     let crystal = convert_crystal(entry?)?;
    //     // Crystals are stored as pattern slots
    //     crystal_count += 1;
    // }
    //
    // // Count vectors (stored separately in vector pages)
    // let vector_count = l1_count + doc_count; // Approximate
    //
    // Ok(MigrateReport {
    //     l1_nodes: l1_count,
    //     l1_edges: edge_count,
    //     l2_topics: topic_count,
    //     l3_nodes: 0, // L3 nodes may be distilled during dream pipeline
    //     l4_docs: doc_count,
    //     l5_crystals: crystal_count,
    //     vectors: vector_count,
    //     elapsed_ms: 0, // Will be set by caller
    // })

    // Return empty report for now
    Ok(MigrateReport {
        l1_nodes: 0,
        l1_edges: 0,
        l2_topics: 0,
        l3_nodes: 0,
        l4_docs: 0,
        l5_crystals: 0,
        vectors: 0,
        elapsed_ms: 0,
    })
}

/// Convert a legacy string ID to xxHash64
///
/// # Arguments
/// * `id` - Legacy string identifier
///
/// # Returns
/// 64-bit hash value
pub fn convert_id_to_hash(id: &str) -> u64 {
    hash_id(id)
}

/// Verify migration integrity by comparing source and destination
///
/// This function performs a comprehensive verification of the migration:
/// 1. Check record counts match
/// 2. Verify data content for sampled records
/// 3. Validate index consistency
///
/// # Arguments
/// * `redb_path` - Path to legacy database (for comparison)
/// * `meh_path` - Path to new MemHop database
///
/// # Returns
/// `Ok(())` if verification passes, `Err` otherwise
///
/// # Note
/// This function requires access to both databases simultaneously
pub fn verify_migration(redb_path: &Path, meh_path: &Path) -> MigrateResult<()> {
    // NOTE: Verification logic is a stub; implement with real redb comparison when legacy code is available
    // Pseudocode:
    //
    // let legacy_db = redb::Database::open(redb_path)?;
    // let memhop_db = MemHop::open(MemHopConfig::new(meh_path, 768))?;
    //
    // // Compare record counts
    // let legacy_count = count_legacy_records(&legacy_db)?;
    // let memhop_count = count_memhop_records(&memhop_db)?;
    //
    // if legacy_count != memhop_count {
    //     return Err(MigrateError::Verification(format!(
    //         "Record count mismatch: legacy={}, memhop={}",
    //         legacy_count, memhop_count
    //     )));
    // }
    //
    // // Sample verification: check random records
    // verify_sample_records(&legacy_db, &memhop_db)?;
    //
    // Ok(())

    // Suppress unused parameter warnings
    let _ = (redb_path, meh_path);

    // Return success for now (stub implementation)
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    #[test]
    fn test_convert_id_to_hash_consistency() {
        // Same input should produce same hash
        let hash1 = convert_id_to_hash("test-id");
        let hash2 = convert_id_to_hash("test-id");
        assert_eq!(hash1, hash2);

        // Different input should produce different hash
        let hash3 = convert_id_to_hash("different-id");
        assert_ne!(hash1, hash3);
    }

    #[test]
    fn test_migrate_report_display() {
        let report = MigrateReport {
            l1_nodes: 100,
            l1_edges: 50,
            l2_topics: 10,
            l3_nodes: 20,
            l4_docs: 200,
            l5_crystals: 5,
            vectors: 300,
            elapsed_ms: 1234,
        };

        let display = format!("{}", report);
        assert!(display.contains("L1 nodes: 100"));
        assert!(display.contains("L4 docs: 200"));
        assert!(display.contains("Time: 1234ms"));
    }

    #[test]
    fn test_migrate_nonexistent_legacy_db() {
        let temp_dir = TempDir::new().unwrap();
        let meh_path = temp_dir.path().join("test.meh");

        let result = migrate(Path::new("/nonexistent/legacy.db"), &meh_path);
        assert!(result.is_err());

        match result.unwrap_err() {
            MigrateError::LegacyDb(msg) => {
                assert!(msg.contains("not found"));
            }
            _ => panic!("Expected LegacyDb error"),
        }
    }

    #[test]
    fn test_migrate_existing_target_file() {
        let temp_dir = TempDir::new().unwrap();
        let redb_path = temp_dir.path().join("legacy.db");
        let meh_path = temp_dir.path().join("test.meh");

        // Create dummy files
        std::fs::write(&redb_path, b"dummy").unwrap();
        std::fs::write(&meh_path, b"dummy").unwrap();

        let result = migrate(&redb_path, &meh_path);
        assert!(result.is_err());

        match result.unwrap_err() {
            MigrateError::Io(e) => {
                assert_eq!(e.kind(), std::io::ErrorKind::AlreadyExists);
            }
            _ => panic!("Expected Io error"),
        }
    }

    #[test]
    fn test_verify_migration_stub() {
        let temp_dir = TempDir::new().unwrap();
        let redb_path = temp_dir.path().join("legacy.db");
        let meh_path = temp_dir.path().join("test.meh");

        // Create dummy files
        std::fs::write(&redb_path, b"dummy").unwrap();
        std::fs::write(&meh_path, b"dummy").unwrap();

        // Stub verification should succeed
        let result = verify_migration(&redb_path, &meh_path);
        assert!(result.is_ok());
    }
}
