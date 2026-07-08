// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// L2 — Scene-level + Topic-level nodes with dual-track user/agent timelines.
//
// Tree structure: parent_id (None = depth-1 root) + children_ids.
// Depth 1 = raw conversation turn, 2 = compressed group, 3 = meta summary;
// depth >= 4 triggers subtree deletion during dream compression.
//
// SceneSlot: lightweight per-scene metadata (scene_id + scene_name).
// TopicSlot: dual-track conversation node with user/agent keywords,
//   timestamps, L4/L3 references, and optional fused compression fields.

use crate::util::io_helpers::*;
use serde::{Deserialize, Serialize};
use std::io::{self, Cursor, Read, Write};

// ============================================================================
// SceneSlot — per-scene metadata
// ============================================================================

#[derive(Debug, Clone, PartialEq)]
pub struct SceneSlot {
    pub scene_id: u64,
    pub scene_name: String,
}

impl SceneSlot {
    pub fn new(scene_name: &str) -> Self {
        Self {
            scene_id: crate::util::hash_id(scene_name),
            scene_name: scene_name.to_string(),
        }
    }

    pub fn slot_size(&self) -> usize {
        10 + self.scene_name.len() // scene_id(8) + name_len(2) + name
    }

    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buf = Vec::with_capacity(self.slot_size());
        buf.write_all(&self.scene_id.to_le_bytes())?;
        buf.write_all(&(self.scene_name.len() as u16).to_le_bytes())?;
        buf.write_all(self.scene_name.as_bytes())?;
        Ok(buf)
    }

    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut c = Cursor::new(data);
        let scene_id = read_u64(&mut c)?;
        let name_len = read_u16(&mut c)? as usize;
        if 10 + name_len > data.len() {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "SceneSlot name exceeds data",
            ));
        }
        let mut name_buf = vec![0u8; name_len];
        c.read_exact(&mut name_buf)?;
        let scene_name = String::from_utf8(name_buf)
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
        Ok(Self {
            scene_id,
            scene_name,
        })
    }
}

// ============================================================================
// TopicSlot — dual-track conversation node
// ============================================================================

/// v4 binary format (87-byte fixed header):
///
/// ```text
/// id:             u64   (8)
/// scene_id:       u64   (8)
/// parent_id:      u64   (8)   // 0 = None
/// depth:          u8    (1)
/// children_count: u16   (2)
/// user_kw_count:  u16   (2)
/// user_l4_count:  u16   (2)
/// user_l3_count:  u16   (2)
/// agent_kw_count: u16   (2)
/// agent_l4_count: u16   (2)
/// agent_l3_count: u16   (2)
/// fused_kw_count: u16   (2)
/// fused_summary_len: u16 (2)
/// user_timestamp: i64   (8)
/// agent_timestamp:i64   (8)
/// centroid:       u64   (8)
/// created_at:     i64   (8)
/// updated_at:     i64   (8)
/// version:        u32   (4)
/// ```
///
/// Variable section follows in this order:
///   children_ids (N×8), user_keywords (each: u16 len + utf8),
///   user_l4_refs (N×8), user_l3_refs (N×8),
///   agent_keywords (each: u16 len + utf8),
///   agent_l4_refs (N×8), agent_l3_refs (N×8),
///   fused_keywords (each: u16 len + utf8),
///   fused_summary (raw utf8).

#[derive(Debug, Clone, PartialEq)]
pub struct TopicSlot {
    pub id: u64,
    pub scene_id: u64,
    pub parent_id: Option<u64>,
    pub children_ids: Vec<u64>,
    pub depth: u8,

    // ── User track ──
    pub user_keywords: Vec<String>,
    pub user_timestamp: i64,
    pub user_l4_refs: Vec<u64>,
    pub user_l3_refs: Vec<u64>,

    // ── Agent track ──
    pub agent_keywords: Vec<String>,
    pub agent_timestamp: i64,
    pub agent_l4_refs: Vec<u64>,
    pub agent_l3_refs: Vec<u64>,

    // ── Compression fields (depth >= 2) ──
    pub fused_keywords: Vec<String>,
    pub fused_summary: Option<String>,

    // ── Retrieval ──
    pub centroid_page_ref: u64,

    // ── Metadata ──
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
}

impl TopicSlot {
    /// Fixed header size in bytes.
    pub const FIXED_SIZE: usize = 87;

    /// Create a new depth-1 turn node with idempotent ID.
    pub fn new_turn(
        scene_id: u64,
        user_keywords: Vec<String>,
        user_timestamp: i64,
        user_l4_refs: Vec<u64>,
        user_l3_refs: Vec<u64>,
        agent_keywords: Vec<String>,
        agent_timestamp: i64,
        agent_l4_refs: Vec<u64>,
        agent_l3_refs: Vec<u64>,
        created_at: i64,
    ) -> Self {
        let id = Self::compute_id(scene_id, user_timestamp, agent_timestamp);
        Self {
            id,
            scene_id,
            parent_id: None,
            children_ids: vec![],
            depth: 1,
            user_keywords,
            user_timestamp,
            user_l4_refs,
            user_l3_refs,
            agent_keywords,
            agent_timestamp,
            agent_l4_refs,
            agent_l3_refs,
            fused_keywords: vec![],
            fused_summary: None,
            centroid_page_ref: 0,
            created_at,
            updated_at: created_at,
            version: 4,
        }
    }

    /// Idempotent ID: hash of (scene_id, user_ts, agent_ts).
    pub fn compute_id(scene_id: u64, user_ts: i64, agent_ts: i64) -> u64 {
        let combined = format!("{}:{}:{}", scene_id, user_ts, agent_ts);
        crate::util::hash_id(&combined)
    }

    // ----- helpers for variable-length keyword arrays -----

    fn write_keyword_array(buf: &mut Vec<u8>, kws: &[String]) -> io::Result<()> {
        for kw in kws {
            buf.write_all(&(kw.len() as u16).to_le_bytes())?;
            buf.write_all(kw.as_bytes())?;
        }
        Ok(())
    }

    fn read_keyword_array(c: &mut Cursor<&[u8]>, count: usize) -> io::Result<Vec<String>> {
        let mut out = Vec::with_capacity(count);
        for _ in 0..count {
            let len = read_u16(c)? as usize;
            let mut buf = vec![0u8; len];
            c.read_exact(&mut buf)?;
            out.push(
                String::from_utf8(buf)
                    .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?,
            );
        }
        Ok(out)
    }

    fn keyword_array_byte_size(kws: &[String]) -> usize {
        kws.iter().map(|k| 2 + k.len()).sum::<usize>()
    }

    // ----- serialize / deserialize -----

    pub fn slot_size(&self) -> usize {
        Self::FIXED_SIZE
            + self.children_ids.len() * 8
            + Self::keyword_array_byte_size(&self.user_keywords)
            + self.user_l4_refs.len() * 8
            + self.user_l3_refs.len() * 8
            + Self::keyword_array_byte_size(&self.agent_keywords)
            + self.agent_l4_refs.len() * 8
            + self.agent_l3_refs.len() * 8
            + Self::keyword_array_byte_size(&self.fused_keywords)
            + self.fused_summary.as_ref().map_or(0, |s| s.len())
    }

    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buf = Vec::with_capacity(self.slot_size());

        // Fixed header
        buf.write_all(&self.id.to_le_bytes())?;
        buf.write_all(&self.scene_id.to_le_bytes())?;
        buf.write_all(&self.parent_id.unwrap_or(0).to_le_bytes())?;
        buf.write_all(&[self.depth])?;
        buf.write_all(&(self.children_ids.len() as u16).to_le_bytes())?;
        buf.write_all(&(self.user_keywords.len() as u16).to_le_bytes())?;
        buf.write_all(&(self.user_l4_refs.len() as u16).to_le_bytes())?;
        buf.write_all(&(self.user_l3_refs.len() as u16).to_le_bytes())?;
        buf.write_all(&(self.agent_keywords.len() as u16).to_le_bytes())?;
        buf.write_all(&(self.agent_l4_refs.len() as u16).to_le_bytes())?;
        buf.write_all(&(self.agent_l3_refs.len() as u16).to_le_bytes())?;
        buf.write_all(&(self.fused_keywords.len() as u16).to_le_bytes())?;
        buf.write_all(
            &(self.fused_summary.as_ref().map_or(0u16, |s| s.len() as u16)).to_le_bytes(),
        )?;
        buf.write_all(&self.user_timestamp.to_le_bytes())?;
        buf.write_all(&self.agent_timestamp.to_le_bytes())?;
        buf.write_all(&self.centroid_page_ref.to_le_bytes())?;
        buf.write_all(&self.created_at.to_le_bytes())?;
        buf.write_all(&self.updated_at.to_le_bytes())?;
        buf.write_all(&self.version.to_le_bytes())?;

        // Variable section (order must match deserialize)
        for &child in &self.children_ids {
            buf.write_all(&child.to_le_bytes())?;
        }
        Self::write_keyword_array(&mut buf, &self.user_keywords)?;
        for &r in &self.user_l4_refs {
            buf.write_all(&r.to_le_bytes())?;
        }
        for &r in &self.user_l3_refs {
            buf.write_all(&r.to_le_bytes())?;
        }
        Self::write_keyword_array(&mut buf, &self.agent_keywords)?;
        for &r in &self.agent_l4_refs {
            buf.write_all(&r.to_le_bytes())?;
        }
        for &r in &self.agent_l3_refs {
            buf.write_all(&r.to_le_bytes())?;
        }
        Self::write_keyword_array(&mut buf, &self.fused_keywords)?;
        if let Some(ref s) = self.fused_summary {
            buf.write_all(s.as_bytes())?;
        }

        Ok(buf)
    }

    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut c = Cursor::new(data);

        let id = read_u64(&mut c)?;
        let scene_id = read_u64(&mut c)?;
        let parent_val = read_u64(&mut c)?;
        let parent_id = if parent_val == 0 {
            None
        } else {
            Some(parent_val)
        };
        let depth = read_u8(&mut c)?;
        let children_count = read_u16(&mut c)? as usize;
        let user_kw_count = read_u16(&mut c)? as usize;
        let user_l4_count = read_u16(&mut c)? as usize;
        let user_l3_count = read_u16(&mut c)? as usize;
        let agent_kw_count = read_u16(&mut c)? as usize;
        let agent_l4_count = read_u16(&mut c)? as usize;
        let agent_l3_count = read_u16(&mut c)? as usize;
        let fused_kw_count = read_u16(&mut c)? as usize;
        let fused_summary_len = read_u16(&mut c)? as usize;
        let user_timestamp = read_i64(&mut c)?;
        let agent_timestamp = read_i64(&mut c)?;
        let centroid_page_ref = read_u64(&mut c)?;
        let created_at = read_i64(&mut c)?;
        let updated_at = read_i64(&mut c)?;
        let version = read_u32(&mut c)?;

        // Basic sanity check on variable section
        // (exact validation happens per-field below)
        if data.len() < Self::FIXED_SIZE {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "TopicSlot too short for v4 header",
            ));
        }

        // Variable section in order
        let mut children_ids = Vec::with_capacity(children_count);
        for _ in 0..children_count {
            children_ids.push(read_u64(&mut c)?);
        }
        let user_keywords = Self::read_keyword_array(&mut c, user_kw_count)?;
        let mut user_l4_refs = Vec::with_capacity(user_l4_count);
        for _ in 0..user_l4_count {
            user_l4_refs.push(read_u64(&mut c)?);
        }
        let mut user_l3_refs = Vec::with_capacity(user_l3_count);
        for _ in 0..user_l3_count {
            user_l3_refs.push(read_u64(&mut c)?);
        }
        let agent_keywords = Self::read_keyword_array(&mut c, agent_kw_count)?;
        let mut agent_l4_refs = Vec::with_capacity(agent_l4_count);
        for _ in 0..agent_l4_count {
            agent_l4_refs.push(read_u64(&mut c)?);
        }
        let mut agent_l3_refs = Vec::with_capacity(agent_l3_count);
        for _ in 0..agent_l3_count {
            agent_l3_refs.push(read_u64(&mut c)?);
        }
        let fused_keywords = Self::read_keyword_array(&mut c, fused_kw_count)?;
        let fused_summary = if fused_summary_len > 0 {
            let mut buf = vec![0u8; fused_summary_len];
            c.read_exact(&mut buf)?;
            Some(
                String::from_utf8(buf)
                    .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?,
            )
        } else {
            None
        };

        Ok(Self {
            id,
            scene_id,
            parent_id,
            children_ids,
            depth,
            user_keywords,
            user_timestamp,
            user_l4_refs,
            user_l3_refs,
            agent_keywords,
            agent_timestamp,
            agent_l4_refs,
            agent_l3_refs,
            fused_keywords,
            fused_summary,
            centroid_page_ref,
            created_at,
            updated_at,
            version,
        })
    }
}

// ============================================================================
// Legacy aliases (keep external references compiling during transition)
// ============================================================================

/// Legacy alias — use `TopicSlot` directly in new code.
pub type ContextSlot = TopicSlot;

// Keep these for tests that still reference them
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum ActivationState {
    Dormant = 0,
    Active = 1,
    Crystallized = 2,
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

#[derive(Debug, Clone, Copy, PartialEq, Serialize, Deserialize)]
pub struct LlmParams {
    pub temperature: f32,
    pub top_p: f32,
    pub presence_penalty: f32,
    pub frequency_penalty: f32,
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

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    fn make_topic(id: u64, depth: u8) -> TopicSlot {
        TopicSlot {
            id,
            scene_id: 100,
            parent_id: if depth > 1 { Some(1) } else { None },
            children_ids: if depth == 1 { vec![2, 3] } else { vec![] },
            depth,
            user_keywords: vec!["登录".into(), "JWT".into()],
            user_timestamp: 1000,
            user_l4_refs: vec![10],
            user_l3_refs: vec![20],
            agent_keywords: vec!["token".into()],
            agent_timestamp: 1001,
            agent_l4_refs: vec![11],
            agent_l3_refs: vec![21],
            fused_keywords: if depth >= 2 {
                vec!["认证".into()]
            } else {
                vec![]
            },
            fused_summary: if depth >= 2 {
                Some("认证流程讨论".into())
            } else {
                None
            },
            centroid_page_ref: 42,
            created_at: 1000,
            updated_at: 2000,
            version: 4,
        }
    }

    #[test]
    fn test_topic_slot_roundtrip_depth1() {
        let t = make_topic(111, 1);
        let data = t.serialize().unwrap();
        assert_eq!(data.len(), t.slot_size());
        let restored = TopicSlot::deserialize(&data).unwrap();
        assert_eq!(t, restored);
    }

    #[test]
    fn test_topic_slot_roundtrip_depth2() {
        let t = make_topic(222, 2);
        let data = t.serialize().unwrap();
        assert_eq!(data.len(), t.slot_size());
        let restored = TopicSlot::deserialize(&data).unwrap();
        assert_eq!(t, restored);
    }

    #[test]
    fn test_topic_slot_empty_keywords() {
        let t = TopicSlot {
            user_keywords: vec![],
            agent_keywords: vec![],
            fused_keywords: vec![],
            fused_summary: None,
            ..make_topic(333, 1)
        };
        let data = t.serialize().unwrap();
        let restored = TopicSlot::deserialize(&data).unwrap();
        assert_eq!(t, restored);
    }

    #[test]
    fn test_topic_slot_root() {
        let t = TopicSlot {
            parent_id: None,
            depth: 1,
            ..make_topic(444, 1)
        };
        let data = t.serialize().unwrap();
        let restored = TopicSlot::deserialize(&data).unwrap();
        assert_eq!(restored.parent_id, None);
        assert_eq!(restored.depth, 1);
    }

    #[test]
    fn test_topic_slot_unicode() {
        let t = TopicSlot {
            user_keywords: vec!["场景 🚀".into()],
            agent_keywords: vec!["回复内容".into()],
            fused_keywords: vec!["压缩 🔥".into()],
            fused_summary: Some("摘要 📝".into()),
            ..make_topic(555, 2)
        };
        let data = t.serialize().unwrap();
        let restored = TopicSlot::deserialize(&data).unwrap();
        assert_eq!(restored.user_keywords, t.user_keywords);
        assert_eq!(restored.fused_keywords, t.fused_keywords);
        assert_eq!(restored.fused_summary, t.fused_summary);
    }

    #[test]
    fn test_compute_id_deterministic() {
        let id1 = TopicSlot::compute_id(100, 1000, 1001);
        let id2 = TopicSlot::compute_id(100, 1000, 1001);
        assert_eq!(id1, id2);
    }

    #[test]
    fn test_compute_id_different() {
        let id1 = TopicSlot::compute_id(100, 1000, 1001);
        let id2 = TopicSlot::compute_id(100, 1000, 1002);
        assert_ne!(id1, id2);
    }

    #[test]
    fn test_scene_slot_roundtrip() {
        let s = SceneSlot::new("测试场景");
        let data = s.serialize().unwrap();
        assert_eq!(data.len(), s.slot_size());
        let restored = SceneSlot::deserialize(&data).unwrap();
        assert_eq!(s, restored);
        assert_eq!(restored.scene_name, "测试场景");
    }
}
