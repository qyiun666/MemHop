// Dream consolidation pipeline (prune module)
use crate::dream::llm::LlmProvider;
use crate::file::header::FileHeader;
use crate::index::btree::BTreeIndex;
use crate::index::sparse::SparseIndex;
use crate::MemHopError;
use memmap2::MmapMut;
use std::collections::HashSet;

/// Configuration for dream operation
pub struct DreamConfig {
    /// Whether to compress L2 topics (semantic compression)
    pub compress_l2: bool,
    /// Whether to distill L3 procedural memories
    pub distill_l3: bool,
    /// Whether to crystallize L5 programmatic knowledge
    pub crystallize_l5: bool,
    /// Importance threshold below which documents are pruned
    pub prune_threshold: f32,
    /// Time window (start_timestamp, end_timestamp) in milliseconds
    pub time_window: (i64, i64),
}

/// Report of dream operation
pub struct DreamReport {
    /// New topics created during consolidation
    pub new_topics: Vec<String>,
    /// New domain nodes created (L3 procedural memories)
    pub new_domain_nodes: Vec<String>,
    /// New crystals created (L5 programmatic knowledge)
    pub new_crystals: Vec<String>,
    /// Merged topics: (absorbed_topic_id, keeper_topic_id)
    pub merged_topics: Vec<(String, String)>,
    /// Pruned documents/enhrams
    pub pruned: Vec<String>,
    /// Topics demoted to dormant state
    pub demoted_to_dormant: Vec<String>,
    /// New temporal edges created
    pub new_temporal_edges: Vec<String>,
    /// New co-occurrence edges created
    pub new_cooccurrence_edges: Vec<String>,
}

/// Run dream consolidation pipeline
/// Scans L1 Engrams and performs cleanup based on importance and age
pub fn dream_consolidation(
    mmap: &mut MmapMut,
    config: DreamConfig,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    sparse_index: &SparseIndex,
    llm: &dyn LlmProvider,
    session_topic_ids: HashSet<u64>,
) -> Result<DreamReport, MemHopError> {
    // Delegate to main orchestration function
    crate::dream::dream_pipeline(mmap, config, header, btree, sparse_index, llm, session_topic_ids)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::util::PAGE_SIZE;
    use std::io::Write;

    #[test]
    fn test_dream_config_structure() {
        let config = DreamConfig {
            compress_l2: true,
            distill_l3: false,
            crystallize_l5: true,
            prune_threshold: 0.1,
            time_window: (0, 1000000),
        };

        assert!(config.compress_l2);
        assert!(!config.distill_l3);
        assert!(config.crystallize_l5);
        assert!((config.prune_threshold - 0.1).abs() < 0.001);
        assert_eq!(config.time_window, (0, 1000000));
    }

    #[test]
    fn test_dream_report_structure() {
        let report = DreamReport {
            new_topics: vec!["topic-1".to_string()],
            new_domain_nodes: vec!["domain-1".to_string()],
            new_crystals: vec!["crystal-1".to_string()],
            merged_topics: vec![("old-topic".to_string(), "new-topic".to_string())],
            pruned: vec!["doc-1".to_string()],
            demoted_to_dormant: vec!["topic-2".to_string()],
            new_temporal_edges: vec!["edge-1".to_string()],
            new_cooccurrence_edges: vec!["edge-2".to_string()],
        };

        assert_eq!(report.new_topics.len(), 1);
        assert_eq!(report.merged_topics.len(), 1);
        assert_eq!(report.pruned.len(), 1);
    }

    #[test]
    fn test_dream_consolidation_empty() {
        // Test with empty database
        let temp_file = tempfile::NamedTempFile::new().unwrap();
        let path = temp_file.path();

        let mut file = std::fs::File::create(path).unwrap();
        file.write_all(&vec![0u8; PAGE_SIZE * 50]).unwrap();
        drop(file);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = crate::file::header::FileHeader::new(768);
        let mut btree = crate::index::btree::BTreeIndex::new();
        let sparse_index = crate::index::sparse::SparseIndex::new();
        
        // Create a mock LLM provider
        struct MockLlm;
        impl crate::dream::llm::LlmProvider for MockLlm {
            fn summarize(&self, _texts: &[String]) -> Result<String, crate::MemHopError> {
                Ok("mock summary".to_string())
            }
            fn extract_patterns(&self, _memories: &[crate::dream::llm::MemorySummary]) -> Result<Vec<crate::dream::llm::Pattern>, crate::MemHopError> {
                Ok(vec![])
            }
            fn generate_crystal(&self, _pattern: &crate::dream::llm::Pattern) -> Result<crate::dream::llm::CrystalDef, crate::MemHopError> {
                Ok(crate::dream::llm::CrystalDef {
                    condition: "mock".to_string(),
                    action: "mock".to_string(),
                    confidence: 0.5,
                })
            }
            fn fallback_summarize(&self, _texts: &[String]) -> String {
                "mock".to_string()
            }
            fn fallback_extract_patterns(&self, _memories: &[crate::dream::llm::MemorySummary]) -> Vec<crate::dream::llm::Pattern> {
                vec![]
            }
            fn fallback_generate_crystal(&self, _pattern: &crate::dream::llm::Pattern) -> crate::dream::llm::CrystalDef {
                crate::dream::llm::CrystalDef {
                    condition: "mock".to_string(),
                    action: "mock".to_string(),
                    confidence: 0.3,
                }
            }
        }
        let llm = MockLlm;

        let config = DreamConfig {
            compress_l2: false,
            distill_l3: false,
            crystallize_l5: false,
            prune_threshold: 0.5,
            time_window: (0, i64::MAX),
        };

        let report = dream_consolidation(
            &mut mmap, 
            config, 
            &mut header, 
            &mut btree, 
            &sparse_index, 
            &llm,
            std::collections::HashSet::new()
        ).unwrap();

        // Should return zero counts for empty database
        assert_eq!(report.pruned.len(), 0);
        assert_eq!(report.new_topics.len(), 0);
    }
}
