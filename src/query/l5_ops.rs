// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! L5 ActionChain CRUD internal implementation.

use crate::file::header::FileHeader;
use crate::file::page::decode_page_ref;
use crate::index::btree::BTreeIndex;
use crate::layers::action_chain::{ActionChainSlot, ActionStep, ChainStatus};
use crate::query::types::UpdateL5Fields;
use crate::shared::common::{now_ms, parse_id_to_hash};
use crate::shared::slot_io::{decode_page_id, get_slot_data};
use crate::util::{PageType, PAGE_SIZE};
use crate::MemHopError;
use memmap2::MmapMut;

/// Get an L5 action chain by ID.
pub fn get_l5(
    mmap: &MmapMut,
    btree: &BTreeIndex,
    id: &str,
) -> Result<Option<ActionChainSlot>, MemHopError> {
    let id_hash = parse_id_to_hash(id);
    match btree.search(id_hash) {
        Some(page_ref) => {
            let data: &[u8] = &mmap[..];
            let slot_data = get_slot_data(data, page_ref)
                .ok_or_else(|| MemHopError::PageNotFound(decode_page_id(page_ref)))?;
            Ok(Some(ActionChainSlot::deserialize_slot(slot_data)?))
        }
        None => Ok(None),
    }
}

/// Partially update an L5 action chain.
pub fn update_l5(
    mmap: &mut MmapMut,
    _header: &mut FileHeader,
    btree: &BTreeIndex,
    id: &str,
    fields: UpdateL5Fields,
) -> Result<(), MemHopError> {
    let id_hash = parse_id_to_hash(id);
    let page_ref = btree.search(id_hash).ok_or(MemHopError::PageNotFound(0))?;
    let page_id = decode_page_id(page_ref);
    let offset = crate::shared::slot_io::slot_offset(page_id);

    let mut chain = ActionChainSlot::deserialize_slot(&mmap[offset..])?;

    if let Some(title) = fields.title {
        chain.title = title;
    }
    if let Some(trigger) = fields.trigger {
        chain.trigger = trigger;
    }
    if let Some(status) = fields.status {
        chain.status = parse_chain_status(&status);
    }
    if let Some(confidence) = fields.confidence {
        chain.confidence = confidence;
    }
    if let Some(success_rate) = fields.success_rate {
        chain.success_rate = success_rate;
    }
    if let Some(trigger_count) = fields.trigger_count {
        chain.trigger_count = trigger_count;
    }
    if let Some(last_triggered) = fields.last_triggered {
        chain.last_triggered = last_triggered;
    }

    chain.updated_at = now_ms();
    chain.version += 1;

    let data = chain
        .serialize()
        .map_err(|e| MemHopError::Serialization(e.to_string()))?;
    if offset + data.len() > mmap.len() {
        return Err(MemHopError::PageNotFound(page_id));
    }
    mmap[offset..offset + data.len()].copy_from_slice(&data);

    Ok(())
}

fn parse_chain_status(s: &str) -> ChainStatus {
    match s.to_lowercase().as_str() {
        "active" => ChainStatus::Active,
        "deprecated" => ChainStatus::Deprecated,
        _ => ChainStatus::Draft,
    }
}

/// Delete an L5 action chain and all its steps.
pub fn delete_l5(
    mmap: &mut MmapMut,
    header: &mut FileHeader,
    btree: &mut BTreeIndex,
    id: &str,
) -> Result<(), MemHopError> {
    let chain_id = parse_id_to_hash(id);
    let chain_page_ref = match btree.search(chain_id) {
        Some(pr) => pr,
        None => return Ok(()),
    };

    let chain_page_id = decode_page_id(chain_page_ref);
    crate::file::free_list::free_page(mmap, header, chain_page_id)?;
    btree.delete(chain_id);

    let data: &[u8] = &mmap[..];
    let mut steps: Vec<(u64, u64)> = Vec::new();
    for (&id_hash, &page_ref) in btree.iter_unsorted() {
        if page_type(data, decode_page_id(page_ref)) != Some(PageType::ActionStep as u16) {
            continue;
        }
        if let Some(slot_data) = get_slot_data(data, page_ref) {
            if let Ok(step) = ActionStep::deserialize(slot_data) {
                if step.chain_id == chain_id {
                    steps.push((id_hash, page_ref));
                }
            }
        }
    }

    for (step_hash, page_ref) in steps {
        btree.delete(step_hash);
        crate::file::free_list::free_page(mmap, header, decode_page_ref(page_ref).0)?;
    }

    Ok(())
}

#[inline]
fn page_type(data: &[u8], page_id: u32) -> Option<u16> {
    let offset = (page_id as usize) * PAGE_SIZE + 4;
    if offset + 2 > data.len() {
        return None;
    }
    Some(u16::from_le_bytes([data[offset], data[offset + 1]]))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::file::page::{allocate_page, write_page_data};
    use crate::test_helpers::create_test_mmap;

    #[test]
    fn test_l5_crud() {
        let (mut mmap, mut header, mut btree, mut file) = create_test_mmap(64);

        let chain = ActionChainSlot {
            id_hash: 5001,
            title: "deploy".into(),
            trigger: "keyword deploy".into(),
            status: ChainStatus::Draft,
            confidence: 0.5,
            success_rate: 0.9,
            trigger_count: 0,
            last_triggered: 0,
            created_at: 0,
            updated_at: 0,
            version: 1,
        };
        let page_id = allocate_page(
            &mut mmap,
            &mut header,
            PageType::ActionChain,
            5,
            crate::index::btree::EMPTY_PAGE,
            &mut file,
        )
        .unwrap();
        write_page_data(&mut mmap, page_id, &chain.serialize().unwrap()).unwrap();
        btree.insert(5001, (page_id as u64) << 16);

        let got = get_l5(&mmap, &btree, "0000000000001389")
            .unwrap()
            .expect("chain should exist");
        assert_eq!(got.title, "deploy");

        update_l5(
            &mut mmap,
            &mut header,
            &btree,
            "0000000000001389",
            UpdateL5Fields {
                title: Some("deploy service".into()),
                status: Some("active".into()),
                ..Default::default()
            },
        )
        .unwrap();

        let updated = get_l5(&mmap, &btree, "0000000000001389").unwrap().unwrap();
        assert_eq!(updated.title, "deploy service");
        assert_eq!(updated.status, ChainStatus::Active);

        delete_l5(&mut mmap, &mut header, &mut btree, "0000000000001389").unwrap();
        assert!(get_l5(&mmap, &btree, "0000000000001389").unwrap().is_none());
    }
}
