// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! V2 append-only storage engine module.

pub mod backend;
pub mod engine;
pub mod record;

pub use backend::{MmapBackend, StorageBackend};
pub use engine::StorageEngine;
pub use record::*;
