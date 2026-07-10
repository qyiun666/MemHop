// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! API-4: L0 Profile operations.

use crate::query::types::ProfileResult;
use crate::{MemHop, Result};

impl MemHop {
    /// Get the L0 agent profile.
    pub fn get_profile(&self) -> Result<Option<ProfileResult>> {
        crate::query::profile::read_profile(&self.engine)
    }
}
