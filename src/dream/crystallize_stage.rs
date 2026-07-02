// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Stage: L5 Crystallization — generate procedural knowledge crystals from repeated patterns.

use crate::dream::llm::{LlmProvider, Pattern};
use crate::file::free_list::free_page;
use crate::file::header::FileHeader;
use crate::file::page::{allocate_page, read_page_header, write_page_data};
use crate::index::btree::BTreeIndex;
use crate::slot::action_chain::{ActionChainSlot, ActionStep, ChainStatus};
use crate::util::hash::hash_id;
use crate::util::{PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;
use std::fs::File;

/// Crystallize repeated patterns into L5 action chains using LLM.
pub fn crystallize_patterns(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    llm: &dyn LlmProvider,
    file: &mut File,
) -> Result<Vec<String>, MemHopError> {
    let mut existing_chains: Vec<ActionChainSlot> = Vec::new();
    let page_count = header.page_count;

    for page_id in 18..page_count {
        let offset = (page_id as usize) * PAGE_SIZE;
        if offset + PAGE_SIZE > mmap.len() {
            break;
        }

        if let Ok(page_header) = read_page_header(&mmap[..], page_id) {
            if page_header.page_type != PageType::ActionChain as u16 {
                continue;
            }

            if let Ok(chain) = ActionChainSlot::deserialize(&mmap[offset + 32..]) {
                existing_chains.push(chain);
            }
        }
    }

    if existing_chains.is_empty() {
        return Ok(vec![]);
    }

    existing_chains.sort_by_key(|c| c.created_at);
    let n = std::cmp::min(20, existing_chains.len());
    let recent_chains = &existing_chains[existing_chains.len() - n..];

    let patterns: Vec<Pattern> = recent_chains
        .iter()
        .map(|c| Pattern {
            description: format!("{}: {}", c.title, c.trigger),
            frequency: c.trigger_count.max(1),
            confidence: c.confidence,
        })
        .collect();

    let mut new_crystal_ids = Vec::new();

    for pattern in &patterns {
        let crystal_def = match llm.generate_crystal(pattern) {
            Ok(crystal) => crystal,
            Err(e) => {
                eprintln!("LLM generate_crystal failed, using fallback: {:?}", e);
                llm.fallback_generate_crystal(pattern)
            }
        };

        let now = chrono::Utc::now().timestamp_millis();
        let crystal_chain_id = hash_id(&format!("crystal_{}_{}", crystal_def.condition, now));

        let chain = ActionChainSlot {
            id_hash: crystal_chain_id,
            title: format!("crystal_{}", crystal_def.condition),
            trigger: crystal_def.condition.clone(),
            status: ChainStatus::Draft,
            confidence: crystal_def.confidence,
            success_rate: 0.0,
            trigger_count: 0,
            last_triggered: 0,
            created_at: now,
            updated_at: now,
            version: 1,
        };

        let page_id = allocate_page(mmap, header, PageType::ActionChain, 5, 0, file)?;
        let serialized = chain.serialize()?;
        write_page_data(mmap, page_id, &serialized)?;

        let page_ref = crate::file::page::encode_page_ref(page_id, 0);
        btree.insert(crystal_chain_id, page_ref);

        for (i, step_def) in crystal_def.steps.iter().enumerate() {
            let step_id_hash = hash_id(&format!("step_{}_{}_{}", crystal_chain_id, i, now));
            let step = ActionStep {
                id_hash: step_id_hash,
                chain_id: crystal_chain_id,
                step_order: i as u16,
                action: step_def.action.clone(),
                parameters: step_def.parameters.clone(),
                created_at: now,
            };

            let step_page_id = allocate_page(mmap, header, PageType::ActionStep, 5, 0, file)?;
            let step_data = step
                .serialize()
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;
            write_page_data(mmap, step_page_id, &step_data)?;

            let step_page_ref = crate::file::page::encode_page_ref(step_page_id, 0);
            btree.insert(step_id_hash, step_page_ref);
        }

        new_crystal_ids.push(format!("{:016x}", crystal_chain_id));
    }

    Ok(new_crystal_ids)
}

/// Activate a crystal by validating quality and flipping status to Active.
/// Requires confidence >= 0.5 and at least one linked ActionStep.
pub fn activate_crystal(
    mmap: &mut MmapMut,
    header: &FileHeader,
    btree: &BTreeIndex,
    chain_id: u64,
) -> Result<(), MemHopError> {
    let page_ref = btree.search(chain_id).ok_or_else(|| {
        MemHopError::Serialization(format!("ActionChain {} not found in index", chain_id))
    })?;
    let page_id = crate::query::slot_io::decode_page_id(page_ref);

    if page_id >= header.page_count {
        return Err(MemHopError::PageNotFound(page_id));
    }
    let page_offset = (page_id as usize) * PAGE_SIZE;
    if page_offset + PAGE_SIZE > mmap.len() {
        return Err(MemHopError::PageNotFound(page_id));
    }

    let mut header_bytes = [0u8; 32];
    header_bytes.copy_from_slice(&mmap[page_offset..page_offset + 32]);
    let page_header = crate::file::page::PageHeader::from_bytes(&header_bytes)?;
    if page_header.page_type != PageType::ActionChain as u16 {
        return Err(MemHopError::InvalidPageType);
    }

    let mut chain = ActionChainSlot::deserialize(&mmap[page_offset + 32..])
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;

    if chain.confidence < 0.5 {
        return Err(MemHopError::Serialization(format!(
            "Cannot activate chain {}: confidence {} < 0.5",
            chain_id, chain.confidence
        )));
    }

    let mut step_count = 0;
    for step_page_id in 18..header.page_count {
        let step_offset = (step_page_id as usize) * PAGE_SIZE;
        if step_offset + PAGE_SIZE > mmap.len() {
            break;
        }
        let mut step_header_bytes = [0u8; 32];
        step_header_bytes.copy_from_slice(&mmap[step_offset..step_offset + 32]);
        if let Ok(step_header) = crate::file::page::PageHeader::from_bytes(&step_header_bytes) {
            if step_header.page_type != PageType::ActionStep as u16 {
                continue;
            }
            if let Ok(step) = ActionStep::deserialize(&mmap[step_offset + 32..]) {
                if step.chain_id == chain_id {
                    step_count += 1;
                }
            }
        }
    }

    if step_count == 0 {
        return Err(MemHopError::Serialization(format!(
            "Cannot activate chain {}: no action steps found",
            chain_id
        )));
    }

    chain.status = ChainStatus::Active;
    chain.updated_at = chrono::Utc::now().timestamp_millis();

    let serialized = chain
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    write_page_data(mmap, page_id, &serialized)?;

    Ok(())
}

/// Prune low-quality action chains during dream pipeline.
/// Removes chains with low confidence (< 0.3) and low trigger counts (< 5).
pub fn prune_low_quality_crystals(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    page_count: u32,
) -> Result<Vec<String>, MemHopError> {
    let mut pruned = Vec::new();

    // Skip header pages 0-1 and reserved pages 2-17
    let start_page = 18;
    let end_page = page_count;

    for page_id in start_page..end_page {
        let page_offset = (page_id as usize) * PAGE_SIZE;

        if page_offset + PAGE_SIZE > mmap.len() {
            break;
        }

        if let Ok(page_hdr) = read_page_header(&mmap[..], page_id) {
            if page_hdr.page_type != PageType::ActionChain as u16 {
                continue;
            }
        } else {
            continue;
        }

        let chain_offset = page_offset + 32;
        if let Ok(chain) = ActionChainSlot::deserialize(&mmap[chain_offset..]) {
            // Low confidence + low trigger count → prune
            if chain.confidence < 0.3 && chain.trigger_count < 5 {
                mmap[page_offset..page_offset + PAGE_SIZE].fill(0);
                btree.remove(chain.id_hash);
                free_page(mmap, header, page_id)?;
                pruned.push(format!("{:016x}", chain.id_hash));
            }
        }
    }

    Ok(pruned)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::LlmConfig;
    use crate::dream::llm::{CrystalDef, CrystalStep, LlmProvider, MemorySummary, Pattern};
    use crate::dream::openai_compatible::OpenAICompatibleLlmProvider;
    use crate::file::header::FileHeader;
    use crate::file::page::{allocate_page, write_page_data};
    use crate::util::hash::hash_id;
    use crate::util::PageType;
    use std::io::Write;

    struct MockLlm;

    impl LlmProvider for MockLlm {
        fn summarize(&self, texts: &[String]) -> Result<String, crate::MemHopError> {
            Ok(texts.join(", "))
        }

        fn extract_patterns(
            &self,
            _: &[MemorySummary],
        ) -> Result<Vec<Pattern>, crate::MemHopError> {
            Ok(vec![])
        }

        fn generate_crystal(&self, _: &Pattern) -> Result<CrystalDef, crate::MemHopError> {
            Ok(CrystalDef {
                condition: "when user asks Rust".to_string(),
                action: "provide Rust support".to_string(),
                steps: vec![
                    CrystalStep {
                        action: "extract keywords".to_string(),
                        parameters: Some(r#"{"source":"query"}"#.to_string()),
                    },
                    CrystalStep {
                        action: "retrieve knowledge".to_string(),
                        parameters: Some(r#"{"domain":"rust"}"#.to_string()),
                    },
                    CrystalStep {
                        action: "generate answer".to_string(),
                        parameters: None,
                    },
                ],
                confidence: 0.8,
            })
        }

        fn fallback_summarize(&self, texts: &[String]) -> String {
            texts.join(", ")
        }

        fn fallback_extract_patterns(&self, _: &[MemorySummary]) -> Vec<Pattern> {
            vec![]
        }

        fn fallback_generate_crystal(&self, _: &Pattern) -> CrystalDef {
            CrystalDef {
                condition: "fallback condition".to_string(),
                action: "fallback action".to_string(),
                steps: vec![CrystalStep {
                    action: "fallback step".to_string(),
                    parameters: None,
                }],
                confidence: 0.3,
            }
        }

        fn analyze_user_habits(
            &self,
            _: &[String],
        ) -> Result<crate::dream::llm::HabitAnalysis, crate::MemHopError> {
            Ok(crate::dream::llm::HabitAnalysis::default())
        }

        fn fallback_analyze_user_habits(&self, _: &[String]) -> crate::dream::llm::HabitAnalysis {
            crate::dream::llm::HabitAnalysis::default()
        }

        fn distill_concepts(
            &self,
            _: &str,
        ) -> Result<crate::dream::llm::LlmDistillResult, crate::MemHopError> {
            Ok(crate::dream::llm::LlmDistillResult {
                concepts: vec![],
                relations: vec![],
            })
        }

        fn fallback_distill_concepts(&self, _: &str) -> crate::dream::llm::LlmDistillResult {
            crate::dream::llm::LlmDistillResult {
                concepts: vec![],
                relations: vec![],
            }
        }
    }

    fn setup_file(pages: u32) -> (tempfile::NamedTempFile, MmapMut, FileHeader, BTreeIndex, std::fs::File) {
        let temp_file = tempfile::NamedTempFile::new().unwrap();
        let path = temp_file.path();

        let mut file = std::fs::File::create(path).unwrap();
        file.write_all(&vec![0u8; PAGE_SIZE * pages as usize])
            .unwrap();
        drop(file);

        let file = std::fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(path)
            .unwrap();

        let mut mmap = unsafe { MmapMut::map_mut(&file).unwrap() };
        let mut header = FileHeader::new(768);
        header.page_count = pages;
        crate::file::free_list::init_free_list(&mut header).unwrap();
        for page_id in (18..pages).rev() {
            crate::file::free_list::free_page(&mut mmap, &mut header, page_id).unwrap();
        }

        let btree = BTreeIndex::new();
        (temp_file, mmap, header, btree, file)
    }

    fn write_chain_slot(
        mmap: &mut MmapMut,
        header: &mut FileHeader,
        btree: &mut BTreeIndex,
        chain: ActionChainSlot,
        file: &mut std::fs::File,
    ) -> u32 {
        let page_id = allocate_page(mmap, header, PageType::ActionChain, 5, 0, file).unwrap();
        let data = chain.serialize().unwrap();
        write_page_data(mmap, page_id, &data).unwrap();
        let page_ref = crate::file::page::encode_page_ref(page_id, 0);
        btree.insert(chain.id_hash, page_ref);
        page_id
    }

    fn write_action_step(
        mmap: &mut MmapMut,
        header: &mut FileHeader,
        btree: &mut BTreeIndex,
        step: ActionStep,
        file: &mut std::fs::File,
    ) -> u32 {
        let page_id = allocate_page(mmap, header, PageType::ActionStep, 5, 0, file).unwrap();
        let data = step.serialize().unwrap();
        write_page_data(mmap, page_id, &data).unwrap();
        let page_ref = crate::file::page::encode_page_ref(page_id, 0);
        btree.insert(step.id_hash, page_ref);
        page_id
    }

    fn count_steps_for_chain(
        mmap: &MmapMut,
        header: &FileHeader,
        chain_id: u64,
    ) -> Vec<ActionStep> {
        let mut steps = Vec::new();
        for page_id in 18..header.page_count {
            let offset = (page_id as usize) * PAGE_SIZE;
            if offset + PAGE_SIZE > mmap.len() {
                break;
            }
            let mut header_bytes = [0u8; 32];
            header_bytes.copy_from_slice(&mmap[offset..offset + 32]);
            if let Ok(hdr) = crate::file::page::PageHeader::from_bytes(&header_bytes) {
                if hdr.page_type != PageType::ActionStep as u16 {
                    continue;
                }
                if let Ok(step) = ActionStep::deserialize(&mmap[offset + 32..]) {
                    if step.chain_id == chain_id {
                        steps.push(step);
                    }
                }
            }
        }
        steps.sort_by_key(|s| s.step_order);
        steps
    }

    fn read_chain(mmap: &MmapMut, page_id: u32) -> ActionChainSlot {
        let offset = (page_id as usize) * PAGE_SIZE;
        ActionChainSlot::deserialize(&mmap[offset + 32..]).unwrap()
    }

    #[test]
    fn test_crystallize_patterns_empty() {
        let (_temp, mut mmap, mut header, mut btree, mut file) = setup_file(50);
        let llm = OpenAICompatibleLlmProvider::new(LlmConfig {
            api_url: "https://api.example.com/v1/chat/completions".to_string(),
            api_key: "test-key".to_string(),
            model: "test-model".to_string(),
            ..Default::default()
        });

        let result = crystallize_patterns(&mut mmap, &mut header, &mut btree, &llm, &mut file);
        assert!(result.is_ok());
        assert_eq!(result.unwrap().len(), 0);
    }

    #[test]
    fn test_crystallize_creates_action_steps() {
        let (_temp, mut mmap, mut header, mut btree, mut file) = setup_file(100);
        let now = chrono::Utc::now().timestamp_millis();

        let existing = ActionChainSlot {
            id_hash: hash_id("existing_chain"),
            title: "existing".to_string(),
            trigger: "rust question".to_string(),
            status: ChainStatus::Active,
            confidence: 0.6,
            success_rate: 0.5,
            trigger_count: 3,
            last_triggered: now - 1000,
            created_at: now - 2000,
            updated_at: now - 1000,
            version: 1,
        };
        write_chain_slot(&mut mmap, &mut header, &mut btree, existing, &mut file);

        let llm = MockLlm;
        let result = crystallize_patterns(&mut mmap, &mut header, &mut btree, &llm, &mut file).unwrap();
        assert_eq!(result.len(), 1);

        let crystal_id = u64::from_str_radix(&result[0], 16).unwrap();
        let page_ref = btree.search(crystal_id).unwrap();
        let page_id = crate::query::slot_io::decode_page_id(page_ref);
        let chain = read_chain(&mmap, page_id);
        assert_eq!(chain.status, ChainStatus::Draft);
        assert!((chain.confidence - 0.8).abs() < f32::EPSILON);
        assert_eq!(chain.trigger, "when user asks Rust");

        let steps = count_steps_for_chain(&mmap, &header, crystal_id);
        assert_eq!(steps.len(), 3);
        assert_eq!(steps[0].step_order, 0);
        assert_eq!(steps[0].action, "extract keywords");
        assert_eq!(
            steps[0].parameters,
            Some(r#"{"source":"query"}"#.to_string())
        );
        assert_eq!(steps[1].step_order, 1);
        assert_eq!(steps[1].action, "retrieve knowledge");
        assert_eq!(steps[2].step_order, 2);
        assert_eq!(steps[2].action, "generate answer");
        assert_eq!(steps[2].parameters, None);
    }

    #[test]
    fn test_crystallize_fallback_no_llm() {
        let (_temp, mut mmap, mut header, mut btree, mut file) = setup_file(100);
        let now = chrono::Utc::now().timestamp_millis();

        let existing = ActionChainSlot {
            id_hash: hash_id("fallback_chain"),
            title: "fallback".to_string(),
            trigger: "when user asks Rust then provide support".to_string(),
            status: ChainStatus::Active,
            confidence: 0.6,
            success_rate: 0.5,
            trigger_count: 3,
            last_triggered: now - 1000,
            created_at: now - 2000,
            updated_at: now - 1000,
            version: 1,
        };
        write_chain_slot(&mut mmap, &mut header, &mut btree, existing, &mut file);

        let llm = OpenAICompatibleLlmProvider::new(LlmConfig {
            api_url: "https://api.example.com/v1/chat/completions".to_string(),
            api_key: "test-key".to_string(),
            model: "test-model".to_string(),
            ..Default::default()
        });
        let result = crystallize_patterns(&mut mmap, &mut header, &mut btree, &llm, &mut file).unwrap();
        assert_eq!(result.len(), 1);

        let crystal_id = u64::from_str_radix(&result[0], 16).unwrap();
        let steps = count_steps_for_chain(&mmap, &header, crystal_id);
        assert_eq!(steps.len(), 1);
        assert_eq!(steps[0].action, "provide support");
    }

    #[test]
    fn test_activate_crystal_success() {
        let (_temp, mut mmap, mut header, mut btree, mut file) = setup_file(100);
        let now = chrono::Utc::now().timestamp_millis();

        let chain_id = hash_id("activate_chain");
        let chain = ActionChainSlot {
            id_hash: chain_id,
            title: "activate".to_string(),
            trigger: "test".to_string(),
            status: ChainStatus::Draft,
            confidence: 0.8,
            success_rate: 0.0,
            trigger_count: 0,
            last_triggered: 0,
            created_at: now,
            updated_at: now,
            version: 1,
        };
        let chain_page = write_chain_slot(&mut mmap, &mut header, &mut btree, chain, &mut file);

        let step = ActionStep {
            id_hash: hash_id("activate_step"),
            chain_id,
            step_order: 0,
            action: "do something".to_string(),
            parameters: None,
            created_at: now,
        };
        write_action_step(&mut mmap, &mut header, &mut btree, step, &mut file);

        activate_crystal(&mut mmap, &header, &btree, chain_id).unwrap();

        let activated = read_chain(&mmap, chain_page);
        assert_eq!(activated.status, ChainStatus::Active);
    }

    #[test]
    fn test_activate_crystal_low_confidence_fails() {
        let (_temp, mut mmap, mut header, mut btree, mut file) = setup_file(100);
        let now = chrono::Utc::now().timestamp_millis();

        let chain_id = hash_id("low_conf_chain");
        let chain = ActionChainSlot {
            id_hash: chain_id,
            title: "low".to_string(),
            trigger: "test".to_string(),
            status: ChainStatus::Draft,
            confidence: 0.4,
            success_rate: 0.0,
            trigger_count: 0,
            last_triggered: 0,
            created_at: now,
            updated_at: now,
            version: 1,
        };
        let chain_page = write_chain_slot(&mut mmap, &mut header, &mut btree, chain, &mut file);

        let step = ActionStep {
            id_hash: hash_id("low_conf_step"),
            chain_id,
            step_order: 0,
            action: "do something".to_string(),
            parameters: None,
            created_at: now,
        };
        write_action_step(&mut mmap, &mut header, &mut btree, step, &mut file);

        assert!(activate_crystal(&mut mmap, &header, &btree, chain_id).is_err());

        let chain = read_chain(&mmap, chain_page);
        assert_eq!(chain.status, ChainStatus::Draft);
    }

    #[test]
    fn test_activate_crystal_no_steps_fails() {
        let (_temp, mut mmap, mut header, mut btree, mut file) = setup_file(100);
        let now = chrono::Utc::now().timestamp_millis();

        let chain_id = hash_id("no_step_chain");
        let chain = ActionChainSlot {
            id_hash: chain_id,
            title: "no_steps".to_string(),
            trigger: "test".to_string(),
            status: ChainStatus::Draft,
            confidence: 0.8,
            success_rate: 0.0,
            trigger_count: 0,
            last_triggered: 0,
            created_at: now,
            updated_at: now,
            version: 1,
        };
        write_chain_slot(&mut mmap, &mut header, &mut btree, chain, &mut file);

        assert!(activate_crystal(&mut mmap, &header, &btree, chain_id).is_err());
    }
}
