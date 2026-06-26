// L2 ContextSlot - scene-based conversation context
//
// Each ContextSlot represents a conversation context organized by scene,
// similar to how the human brain categorizes "which scene does this
// conversation belong to".
//
// Supports 4-level nesting via parent_id:
//   Depth 1: Scene            (e.g. "Rust project development")
//   Depth 2: Sub-scene        (e.g. "memhop refactoring discussion")
//   Depth 3: Turn group       (e.g. "L0-L5 layer definition round")
//   Depth 4: Semantic summary (e.g. compressed key points of a turn group)
//
// Each level supports independent compression (multi-turn → summary).

use crate::util::io_helpers::*;
use std::io::{self, Cursor, Read, Write};

/// Context activation state
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum ActivationState {
    Dormant = 0,      // Inactive, no recent interaction
    Active = 1,       // Currently being discussed
    Crystallized = 2, // Consolidated into stable knowledge
}

impl ActivationState {
    pub fn from_u8(v: u8) -> Self {
        match v {
            0 => Self::Dormant,
            1 => Self::Active,
            2 => Self::Crystallized,
            _ => Self::Dormant,
        }
    }
}

/// L2 scene-level recommended LLM parameters (refreshed during dream compression)
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct LlmParams {
    pub temperature: f32,       // 0.0-2.0
    pub top_p: f32,             // 0.0-1.0
    pub presence_penalty: f32,  // -2.0-2.0
    pub frequency_penalty: f32, // -2.0-2.0
}

impl Default for LlmParams {
    fn default() -> Self {
        Self {
            temperature: 0.7,
            top_p: 0.9,
            presence_penalty: 0.0,
            frequency_penalty: 0.0,
        }
    }
}

/// L2 scene context slot
#[derive(Debug, Clone, PartialEq)]
pub struct ContextSlot {
    pub id_hash: u64,
    pub parent_id: Option<u64>, // Parent context (supports 4-level nesting)
    pub depth: u8, // Nesting depth: 1=scene, 2=sub-scene, 3=turn group, 4=semantic summary
    pub title: String, // Scene name
    pub summary: Option<String>, // Compressed summary (multi-turn → compressed)
    pub archive_refs: Vec<u64>, // Associated L4 archives
    pub l3_refs: Vec<u64>, // Associated L3 hypergraph IDs
    pub turn_count: u32, // Number of conversation turns
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
    pub importance: f32,
    pub activation_score: f32,
    pub is_active: bool,
    pub activation_state: ActivationState,
    pub centroid_page_ref: u64,     // Vector page reference (not inline)
    pub dialogue_range: (i64, i64), // (earliest_ts, latest_ts)
    pub llm_params: LlmParams,      // Scene-level LLM parameters
}

impl ContextSlot {
    /// Calculate the total serialized size in bytes
    ///
    /// Fixed v1: 83 bytes (no llm_params)
    /// Fixed v2: 99 bytes (with llm_params: 4 × f32)
    /// Variable: title + summary + archive_refs * 8 + l3_refs * 8
    pub fn slot_size(&self) -> usize {
        let fixed = if self.version >= 2 { 99 } else { 83 };
        fixed
            + self.title.len()  // title
            + self.summary.as_ref().map_or(0, |s| s.len())  // summary
            + self.archive_refs.len() * 8  // archive_refs
            + self.l3_refs.len() * 8 // l3_refs
    }

    /// Serialize to bytes
    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buf = Vec::with_capacity(self.slot_size());

        // Fixed part
        buf.write_all(&self.id_hash.to_le_bytes())?;
        buf.write_all(&self.parent_id.unwrap_or(0).to_le_bytes())?;
        buf.write_all(&[self.depth])?;
        buf.write_all(&(self.title.len() as u16).to_le_bytes())?;

        let summary_len = self.summary.as_ref().map_or(0u16, |s| s.len() as u16);
        buf.write_all(&summary_len.to_le_bytes())?;

        buf.write_all(&(self.archive_refs.len() as u16).to_le_bytes())?;
        buf.write_all(&(self.l3_refs.len() as u16).to_le_bytes())?;
        buf.write_all(&self.turn_count.to_le_bytes())?;

        buf.write_all(&self.created_at.to_le_bytes())?;
        buf.write_all(&self.updated_at.to_le_bytes())?;
        buf.write_all(&self.version.to_le_bytes())?;
        buf.write_all(&self.importance.to_le_bytes())?;
        buf.write_all(&self.activation_score.to_le_bytes())?;
        buf.write_all(&[if self.is_active { 1 } else { 0 }])?;
        buf.write_all(&[self.activation_state as u8])?;
        buf.write_all(&self.centroid_page_ref.to_le_bytes())?;
        buf.write_all(&self.dialogue_range.0.to_le_bytes())?;
        buf.write_all(&self.dialogue_range.1.to_le_bytes())?;

        // LLM params (16 bytes, only for version >= 2)
        if self.version >= 2 {
            buf.write_all(&self.llm_params.temperature.to_le_bytes())?;
            buf.write_all(&self.llm_params.top_p.to_le_bytes())?;
            buf.write_all(&self.llm_params.presence_penalty.to_le_bytes())?;
            buf.write_all(&self.llm_params.frequency_penalty.to_le_bytes())?;
        }

        // Variable part: title
        buf.write_all(self.title.as_bytes())?;

        // Summary
        if let Some(ref summary) = self.summary {
            buf.write_all(summary.as_bytes())?;
        }

        // Archive refs
        for &id in &self.archive_refs {
            buf.write_all(&id.to_le_bytes())?;
        }

        // L3 refs
        for &id in &self.l3_refs {
            buf.write_all(&id.to_le_bytes())?;
        }

        Ok(buf)
    }

    /// Deserialize from bytes
    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut c = Cursor::new(data);

        // Fixed part
        let id_hash = read_u64(&mut c)?;
        let parent_val = read_u64(&mut c)?;
        let parent_id = if parent_val == 0 {
            None
        } else {
            Some(parent_val)
        };
        let depth = read_u8(&mut c)?;

        let title_len = read_u16(&mut c)?;
        let summary_len = read_u16(&mut c)?;
        let archive_count = read_u16(&mut c)? as usize;
        let l3_count = read_u16(&mut c)? as usize;
        let turn_count = read_u32(&mut c)?;

        let created_at = read_i64(&mut c)?;
        let updated_at = read_i64(&mut c)?;
        let version = read_u32(&mut c)?;
        let importance = read_f32(&mut c)?;
        let activation_score = read_f32(&mut c)?;
        let is_active = read_u8(&mut c)? != 0;
        let activation_state = ActivationState::from_u8(read_u8(&mut c)?);
        let centroid_page_ref = read_u64(&mut c)?;
        let dialogue_earliest = read_i64(&mut c)?;
        let dialogue_latest = read_i64(&mut c)?;
        let dialogue_range = (dialogue_earliest, dialogue_latest);

        // LLM params (version >= 2)
        let llm_params = if version >= 2 {
            LlmParams {
                temperature: read_f32(&mut c)?,
                top_p: read_f32(&mut c)?,
                presence_penalty: read_f32(&mut c)?,
                frequency_penalty: read_f32(&mut c)?,
            }
        } else {
            LlmParams::default()
        };

        // Variable part: title
        let mut title_buf = vec![0u8; title_len as usize];
        c.read_exact(&mut title_buf)?;
        let title = String::from_utf8(title_buf)
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;

        // Summary
        let summary = if summary_len > 0 {
            let mut summary_buf = vec![0u8; summary_len as usize];
            c.read_exact(&mut summary_buf)?;
            Some(
                String::from_utf8(summary_buf)
                    .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?,
            )
        } else {
            None
        };

        // Archive refs
        let mut archive_refs = Vec::with_capacity(archive_count);
        for _ in 0..archive_count {
            archive_refs.push(read_u64(&mut c)?);
        }

        // L3 refs
        let mut l3_refs = Vec::with_capacity(l3_count);
        for _ in 0..l3_count {
            l3_refs.push(read_u64(&mut c)?);
        }

        Ok(ContextSlot {
            id_hash,
            parent_id,
            depth,
            title,
            summary,
            archive_refs,
            l3_refs,
            turn_count,
            created_at,
            updated_at,
            version,
            importance,
            activation_score,
            is_active,
            activation_state,
            centroid_page_ref,
            dialogue_range,
            llm_params,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_context_slot_roundtrip() {
        let ctx = ContextSlot {
            id_hash: 123456789,
            parent_id: Some(999),
            depth: 2,
            title: "memhop refactoring".to_string(),
            summary: Some("Refactoring L0-L5 layers".to_string()),
            archive_refs: vec![300, 400],
            l3_refs: vec![100, 200],
            turn_count: 5,
            created_at: 1000000,
            updated_at: 2000000,
            version: 1,
            importance: 0.85,
            activation_score: 0.72,
            is_active: true,
            activation_state: ActivationState::Active,
            centroid_page_ref: 42,
            dialogue_range: (1000000, 2000000),
            llm_params: LlmParams::default(),
        };

        let data = ctx.serialize().unwrap();
        assert_eq!(data.len(), ctx.slot_size());
        let restored = ContextSlot::deserialize(&data).unwrap();
        assert_eq!(ctx, restored);
    }

    #[test]
    fn test_context_slot_root_scene() {
        let ctx = ContextSlot {
            id_hash: 1,
            parent_id: None,
            depth: 1,
            title: "Rust project".to_string(),
            summary: None,
            archive_refs: vec![],
            l3_refs: vec![],
            turn_count: 0,
            created_at: 0,
            updated_at: 0,
            version: 0,
            importance: 0.5,
            activation_score: 0.0,
            is_active: false,
            activation_state: ActivationState::Dormant,
            centroid_page_ref: 0,
            dialogue_range: (0, 0),
            llm_params: LlmParams::default(),
        };

        let data = ctx.serialize().unwrap();
        let restored = ContextSlot::deserialize(&data).unwrap();
        assert_eq!(ctx, restored);
        assert_eq!(restored.parent_id, None);
        assert_eq!(restored.depth, 1);
    }

    #[test]
    fn test_context_slot_three_level_nesting() {
        // Depth 3: turn group within sub-scene within scene
        let ctx = ContextSlot {
            id_hash: 333,
            parent_id: Some(222),
            depth: 3,
            title: "L0-L5 discussion round 1".to_string(),
            summary: Some("Defined L0-L5 field structure".to_string()),
            archive_refs: vec![1001, 1002, 1003],
            l3_refs: vec![501],
            turn_count: 6,
            created_at: 1000,
            updated_at: 2000,
            version: 1,
            importance: 0.9,
            activation_score: 0.8,
            is_active: true,
            activation_state: ActivationState::Active,
            centroid_page_ref: 10,
            dialogue_range: (1000, 2000),
            llm_params: LlmParams::default(),
        };

        let data = ctx.serialize().unwrap();
        let restored = ContextSlot::deserialize(&data).unwrap();
        assert_eq!(ctx, restored);
        assert_eq!(restored.depth, 3);
        assert_eq!(restored.archive_refs.len(), 3);
    }

    #[test]
    fn test_context_slot_size() {
        let ctx = ContextSlot {
            id_hash: 1,
            parent_id: None,
            depth: 1,
            title: "test".to_string(),        // 4 bytes
            summary: Some("abc".to_string()), // 3 bytes
            archive_refs: vec![10],           // 8 bytes
            l3_refs: vec![20, 30],            // 16 bytes
            turn_count: 0,
            created_at: 0,
            updated_at: 0,
            version: 0,
            importance: 0.0,
            activation_score: 0.0,
            is_active: false,
            activation_state: ActivationState::Dormant,
            centroid_page_ref: 0,
            dialogue_range: (0, 0),
            llm_params: LlmParams::default(),
        };

        // 83 + 4 + 3 + 8 + 16 = 114 (version 0, no llm_params in fixed)
        assert_eq!(ctx.slot_size(), 114);
    }

    #[test]
    fn test_context_slot_empty() {
        let ctx = ContextSlot {
            id_hash: 777,
            parent_id: None,
            depth: 1,
            title: "".to_string(),
            summary: None,
            archive_refs: vec![],
            l3_refs: vec![],
            turn_count: 0,
            created_at: 0,
            updated_at: 0,
            version: 0,
            importance: 0.0,
            activation_score: 0.0,
            is_active: false,
            activation_state: ActivationState::Dormant,
            centroid_page_ref: 0,
            dialogue_range: (0, 0),
            llm_params: LlmParams::default(),
        };

        let data = ctx.serialize().unwrap();
        let restored = ContextSlot::deserialize(&data).unwrap();
        assert_eq!(restored, ctx);
    }

    #[test]
    fn test_context_slot_unicode() {
        let ctx = ContextSlot {
            id_hash: 555,
            parent_id: Some(100),
            depth: 2,
            title: "场景测试 🚀".to_string(),
            summary: Some("摘要内容".to_string()),
            archive_refs: vec![1],
            l3_refs: vec![],
            turn_count: 3,
            created_at: 1000,
            updated_at: 1000,
            version: 1,
            importance: 0.7,
            activation_score: 0.6,
            is_active: true,
            activation_state: ActivationState::Active,
            centroid_page_ref: 5,
            dialogue_range: (1000, 1000),
            llm_params: LlmParams::default(),
        };

        let data = ctx.serialize().unwrap();
        let restored = ContextSlot::deserialize(&data).unwrap();
        assert_eq!(restored.title, ctx.title);
        assert_eq!(restored.summary, ctx.summary);
    }
}
