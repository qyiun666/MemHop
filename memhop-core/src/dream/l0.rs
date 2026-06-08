//! dream/l0 — L0 formation: extract worldview/values/personality from L2 topics.

use crate::brain::Brain;
use crate::error::Result;
use crate::profile::L0ProfileStore;

/// Run L0 formation: generate/update L0 profile from current L2 topic state.
/// Returns true if L0 profile was updated.
pub fn l0_formation(brain: &mut Brain) -> Result<bool> {
    L0ProfileStore::dream_form(brain)
}
