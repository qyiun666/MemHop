//! Standalone helper functions for the engine module.

use half::f16;

use crate::types::Protection;

// ── ID generation ─────────────────────────────────────────

pub(crate) fn generate_memory_id_with_tree(tree: &str) -> String {
    use rand::Rng;
    let mut rng = rand::thread_rng();
    let bytes: [u8; 6] = rng.r#gen();
    format!(
        "{}:m_{:02x}{:02x}{:02x}{:02x}{:02x}{:02x}",
        tree, bytes[0], bytes[1], bytes[2], bytes[3], bytes[4], bytes[5]
    )
}

// ── Time helpers ──────────────────────────────────────────

pub(crate) fn now_millis() -> i64 {
    chrono::Utc::now().timestamp_millis()
}

pub(crate) fn millis_to_iso(millis: i64) -> String {
    chrono::DateTime::from_timestamp_millis(millis)
        .map(|dt| dt.to_rfc3339())
        .unwrap_or_default()
}

// ── Protection conversion ─────────────────────────────────

pub(crate) fn protection_to_u8(p: &Protection) -> u8 {
    match p {
        Protection::Normal => 0,
        Protection::Protected => 1,
        Protection::Permanent => 2,
    }
}

// ── Vector helpers ────────────────────────────────────────

/// Convert f16 dense vector to f32 for Hopfield query.
pub(crate) fn f16_to_f32(v: &[f16]) -> Vec<f32> {
    v.iter().map(|x| x.to_f32()).collect()
}
