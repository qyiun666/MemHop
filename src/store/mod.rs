// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! 第 2 层：每层纯 CRUD（纯数据操作）
//!
//! v0.57.0 重构：将 query/ 中的 CRUD 操作拆分到此处。

pub mod l0_store;
pub mod l1_store;
pub mod l2_store;
pub mod l3_store;
pub mod l4_store;
pub mod l5_store;
pub mod l6_store;

use crate::storage::{record::*, StorageEngine};
use crate::{MemHopError, Result};
use serde::{de::DeserializeOwned, Serialize};

/// 通用写入
pub fn write_slot<T: Serialize>(
    engine: &mut StorageEngine,
    record_type: u8,
    id_hash: u64,
    slot: &T,
) -> Result<()> {
    let data = bincode::serialize(slot).map_err(|e| MemHopError::Serialization(e.to_string()))?;
    engine.write_record(record_type, id_hash, &data)?;
    Ok(())
}

/// 通用读取
pub fn read_slot<T: DeserializeOwned>(engine: &StorageEngine, id_hash: u64) -> Result<Option<T>> {
    match engine.read_record(id_hash)? {
        Some((_record_type, data)) => {
            Ok(Some(bincode::deserialize(data).map_err(|e| {
                MemHopError::Deserialization(e.to_string())
            })?))
        }
        None => Ok(None),
    }
}

/// 通用删除
pub fn delete_slot(engine: &mut StorageEngine, id_hash: u64) -> Result<bool> {
    engine.delete_record(id_hash)
}
