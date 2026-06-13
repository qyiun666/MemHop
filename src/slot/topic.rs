// Topic slot serialization (L2 semantic compression)
use std::io::{self, Cursor, Read, Write};

/// Topic activation state
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum ActivationState {
    Dormant = 0,     // Inactive, no recent interaction
    Active = 1,      // Currently being discussed
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

/// L2 Topic slot - semantic compression of conversations
///
/// Parent-child hierarchy:
/// - Parent: compressed summary of user/agent dialogue
/// - Children: original contexts when multiple dialogues discuss same topic
#[derive(Debug, Clone, PartialEq)]
pub struct TopicSlot {
    pub id_hash: u64,                            // Topic unique identifier
    pub title: String,                           // Topic title/label
    pub summary: Option<String>,                 // Optional topic summary
    pub node_ids: Vec<u64>,                      // List of L1 Engram id_hashes
    pub l3_refs: Vec<u64>,                       // Associated L3 Knowledge nodes
    pub l4_refs: Vec<u64>,                       // Associated L4 Archive indices
    pub parent_id: Option<u64>,                  // Parent topic (None = root)
    pub created_at: i64,                         // Creation timestamp (ms)
    pub updated_at: i64,                         // Last update timestamp (ms)
    pub version: u32,                            // Version number
    pub importance: f32,                         // Topic importance [0.0, 1.0]
    pub activation_score: f32,                   // Current activation score [0.0, 1.0]
    // NOTE: is_active and activation_state are related but serve different purposes:
    // - is_active: Boolean flag for quick active/inactive checks (used in queries)
    // - activation_state: Fine-grained state (Dormant/Active/Crystallized) for dream pipeline
    // Keep both for backward compatibility and different use cases.
    pub is_active: bool,                         // Whether topic is currently active
    pub activation_state: ActivationState,       // Activation state (dormant/active/crystallized)
    pub centroid_vector: Option<Vec<half::f16>>, // Centroid vector for topic
    // NOTE: domain_weights is reserved for future domain-based routing features.
    // Currently unused; use l3_refs for knowledge domain associations instead.
    pub domain_weights: Vec<(u64, f32)>,         // (domain_id, weight) pairs - RESERVED
    pub dialogue_range: (i64, i64),              // (earliest_ts, latest_ts)
    // NOTE: reserved field is kept for schema evolution and future extensions
    pub reserved: [u8; 16],                      // Reserved for future extensions
}

impl TopicSlot {
    /// Calculate the total serialized size in bytes
    pub fn slot_size(&self) -> usize {
        // Fixed part: 8 + 2 + 2 + 4 + 8 + 8 + 8 + 4 + 4 + 4 + 1 + 1 + 16 = 70 bytes
        let fixed_size = 70;

        // Variable part: title + summary + node_ids + l3_refs + l4_refs + parent_id + centroid + domain_weights + dialogue
        let title_size = self.title.len();
        let summary_size = self.summary.as_ref().map_or(0, |s| s.len());
        let node_ids_size = self.node_ids.len() * 8;
        let l3_refs_size = 2 + self.l3_refs.len() * 8;
        let l4_refs_size = 2 + self.l4_refs.len() * 8;
        let parent_id_size = 8;

        let centroid_size = match &self.centroid_vector {
            Some(vec) => 1 + 2 + vec.len() * 2,
            None => 1,
        };
        let domain_weights_size = 2 + self.domain_weights.len() * 12;
        let dialogue_range_size = 16;

        fixed_size + title_size + summary_size + node_ids_size
            + l3_refs_size + l4_refs_size + parent_id_size
            + centroid_size + domain_weights_size + dialogue_range_size
    }

    /// Serialize the TopicSlot to bytes
    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buffer = Vec::with_capacity(self.slot_size());

        // Fixed part
        buffer.write_all(&self.id_hash.to_le_bytes())?;
        buffer.write_all(&(self.title.len() as u16).to_le_bytes())?;

        let summary_len = self.summary.as_ref().map_or(0u16, |s| s.len() as u16);
        buffer.write_all(&summary_len.to_le_bytes())?;

        buffer.write_all(&(self.node_ids.len() as u32).to_le_bytes())?;

        let domain_id_val = 0xFFFFFFFFFFFFFFFFu64; // placeholder, l3_refs replaces domain_id
        buffer.write_all(&domain_id_val.to_le_bytes())?;

        buffer.write_all(&self.created_at.to_le_bytes())?;
        buffer.write_all(&self.updated_at.to_le_bytes())?;
        buffer.write_all(&self.version.to_le_bytes())?;
        buffer.write_all(&self.importance.to_le_bytes())?;
        buffer.write_all(&self.activation_score.to_le_bytes())?;
        buffer.write_all(&[if self.is_active { 1 } else { 0 }])?;
        buffer.write_all(&[self.activation_state as u8])?;
        buffer.write_all(&self.reserved)?;

        // Variable part: title
        buffer.write_all(self.title.as_bytes())?;

        // Summary
        if let Some(ref summary) = self.summary {
            buffer.write_all(summary.as_bytes())?;
        }

        // Node IDs
        for node_id in &self.node_ids {
            buffer.write_all(&node_id.to_le_bytes())?;
        }

        // L3 refs
        buffer.write_all(&(self.l3_refs.len() as u16).to_le_bytes())?;
        for &ref_id in &self.l3_refs {
            buffer.write_all(&ref_id.to_le_bytes())?;
        }

        // L4 refs
        buffer.write_all(&(self.l4_refs.len() as u16).to_le_bytes())?;
        for &ref_id in &self.l4_refs {
            buffer.write_all(&ref_id.to_le_bytes())?;
        }

        // Parent ID (use 0 if None)
        let parent_val = self.parent_id.unwrap_or(0);
        buffer.write_all(&parent_val.to_le_bytes())?;

        // Centroid vector
        if let Some(ref vec) = self.centroid_vector {
            buffer.write_all(&[1])?;
            buffer.write_all(&(vec.len() as u16).to_le_bytes())?;
            for &val in vec {
                buffer.write_all(&val.to_le_bytes())?;
            }
        } else {
            buffer.write_all(&[0])?;
        }

        // Domain weights
        buffer.write_all(&(self.domain_weights.len() as u16).to_le_bytes())?;
        for &(domain_id, weight) in &self.domain_weights {
            buffer.write_all(&domain_id.to_le_bytes())?;
            buffer.write_all(&weight.to_le_bytes())?;
        }

        // Dialogue range
        buffer.write_all(&self.dialogue_range.0.to_le_bytes())?;
        buffer.write_all(&self.dialogue_range.1.to_le_bytes())?;

        Ok(buffer)
    }

    /// Deserialize TopicSlot from bytes
    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut cursor = Cursor::new(data);

        let read_u64 = |cursor: &mut Cursor<&[u8]>| -> io::Result<u64> {
            let mut buf = [0u8; 8];
            cursor.read_exact(&mut buf)?;
            Ok(u64::from_le_bytes(buf))
        };

        let read_i64 = |cursor: &mut Cursor<&[u8]>| -> io::Result<i64> {
            let mut buf = [0u8; 8];
            cursor.read_exact(&mut buf)?;
            Ok(i64::from_le_bytes(buf))
        };

        let read_u32 = |cursor: &mut Cursor<&[u8]>| -> io::Result<u32> {
            let mut buf = [0u8; 4];
            cursor.read_exact(&mut buf)?;
            Ok(u32::from_le_bytes(buf))
        };

        let read_u16 = |cursor: &mut Cursor<&[u8]>| -> io::Result<u16> {
            let mut buf = [0u8; 2];
            cursor.read_exact(&mut buf)?;
            Ok(u16::from_le_bytes(buf))
        };

        let read_f32 = |cursor: &mut Cursor<&[u8]>| -> io::Result<f32> {
            let mut buf = [0u8; 4];
            cursor.read_exact(&mut buf)?;
            Ok(f32::from_le_bytes(buf))
        };

        let read_u8 = |cursor: &mut Cursor<&[u8]>| -> io::Result<u8> {
            let mut buf = [0u8; 1];
            cursor.read_exact(&mut buf)?;
            Ok(buf[0])
        };

        // Fixed part
        let id_hash = read_u64(&mut cursor)?;
        let title_len = read_u16(&mut cursor)?;
        let summary_len = read_u16(&mut cursor)?;
        let node_count = read_u32(&mut cursor)?;

        let _domain_id_val = read_u64(&mut cursor)?; // backward compat placeholder

        let created_at = read_i64(&mut cursor)?;
        let updated_at = read_i64(&mut cursor)?;
        let version = read_u32(&mut cursor)?;

        let importance = read_f32(&mut cursor)?;
        let activation_score = read_f32(&mut cursor)?;

        let is_active = read_u8(&mut cursor)? != 0;
        let activation_state = ActivationState::from_u8(read_u8(&mut cursor)?);

        let mut reserved = [0u8; 16];
        cursor.read_exact(&mut reserved)?;

        // Variable part: title
        let mut title_buf = vec![0u8; title_len as usize];
        cursor.read_exact(&mut title_buf)?;
        let title = String::from_utf8(title_buf)
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;

        // Summary
        let summary = if summary_len > 0 {
            let mut summary_buf = vec![0u8; summary_len as usize];
            cursor.read_exact(&mut summary_buf)?;
            Some(String::from_utf8(summary_buf)
                .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?)
        } else {
            None
        };

        // Node IDs
        let mut node_ids = Vec::with_capacity(node_count as usize);
        for _ in 0..node_count {
            node_ids.push(read_u64(&mut cursor)?);
        }

        // L3 refs
        let l3_count = read_u16(&mut cursor)? as usize;
        let mut l3_refs = Vec::with_capacity(l3_count);
        for _ in 0..l3_count {
            l3_refs.push(read_u64(&mut cursor)?);
        }

        // L4 refs
        let l4_count = read_u16(&mut cursor)? as usize;
        let mut l4_refs = Vec::with_capacity(l4_count);
        for _ in 0..l4_count {
            l4_refs.push(read_u64(&mut cursor)?);
        }

        // Parent ID
        let parent_val = read_u64(&mut cursor)?;
        let parent_id = if parent_val == 0 { None } else { Some(parent_val) };

        // Centroid vector
        let centroid_flag = read_u8(&mut cursor)?;
        let centroid_vector = if centroid_flag != 0 {
            let dim = read_u16(&mut cursor)?;
            let mut vec_data = Vec::with_capacity(dim as usize);
            for _ in 0..dim {
                let mut buf = [0u8; 2];
                cursor.read_exact(&mut buf)?;
                vec_data.push(half::f16::from_le_bytes(buf));
            }
            Some(vec_data)
        } else {
            None
        };

        // Domain weights
        let dw_count = read_u16(&mut cursor)?;
        let mut domain_weights = Vec::with_capacity(dw_count as usize);
        for _ in 0..dw_count {
            let domain_id = read_u64(&mut cursor)?;
            let weight = read_f32(&mut cursor)?;
            domain_weights.push((domain_id, weight));
        }

        // Dialogue range
        let earliest_ts = read_i64(&mut cursor)?;
        let latest_ts = read_i64(&mut cursor)?;
        let dialogue_range = (earliest_ts, latest_ts);

        Ok(TopicSlot {
            id_hash, title, summary, node_ids, l3_refs, l4_refs, parent_id,
            created_at, updated_at, version, importance, activation_score,
            is_active, activation_state, centroid_vector, domain_weights, dialogue_range, reserved,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_topic_slot_roundtrip() {
        let topic = TopicSlot {
            id_hash: 123456789,
            title: "Rust Programming".to_string(),
            summary: Some("Topics related to Rust language".to_string()),
            node_ids: vec![1, 2, 3, 4, 5],
            l3_refs: vec![100, 200],
            l4_refs: vec![300],
            parent_id: Some(999),
            created_at: 1000000,
            updated_at: 2000000,
            version: 1,
            importance: 0.85,
            activation_score: 0.72,
            is_active: true,
            activation_state: ActivationState::Active,
            centroid_vector: Some(vec![half::f16::from_f32(0.1); 10]),
            domain_weights: vec![(100, 0.8), (200, 0.6)],
            dialogue_range: (1000000, 2000000),
            reserved: [0; 16],
        };

        let data = topic.serialize().unwrap();
        let deserialized = TopicSlot::deserialize(&data).unwrap();

        assert_eq!(deserialized.id_hash, topic.id_hash);
        assert_eq!(deserialized.title, topic.title);
        assert_eq!(deserialized.summary, topic.summary);
        assert_eq!(deserialized.node_ids, topic.node_ids);
        assert_eq!(deserialized.l3_refs, topic.l3_refs);
        assert_eq!(deserialized.l4_refs, topic.l4_refs);
        assert_eq!(deserialized.parent_id, topic.parent_id);
        assert_eq!(deserialized.created_at, topic.created_at);
        assert_eq!(deserialized.updated_at, topic.updated_at);
        assert_eq!(deserialized.version, topic.version);
        assert!((deserialized.importance - topic.importance).abs() < 1e-6);
        assert!((deserialized.activation_score - topic.activation_score).abs() < 1e-6);
        assert_eq!(deserialized.is_active, topic.is_active);
        assert_eq!(deserialized.activation_state, topic.activation_state);
    }

    #[test]
    fn test_topic_slot_without_refs() {
        let topic = TopicSlot {
            id_hash: 987654321,
            title: "Simple Topic".to_string(),
            summary: None,
            node_ids: vec![],
            l3_refs: vec![],
            l4_refs: vec![],
            parent_id: None,
            created_at: 1000,
            updated_at: 1000,
            version: 1,
            importance: 0.5,
            activation_score: 0.3,
            is_active: false,
            activation_state: ActivationState::Dormant,
            centroid_vector: None,
            domain_weights: vec![],
            dialogue_range: (0, 0),
            reserved: [0; 16],
        };

        let data = topic.serialize().unwrap();
        let deserialized = TopicSlot::deserialize(&data).unwrap();

        assert_eq!(deserialized.id_hash, topic.id_hash);
        assert_eq!(deserialized.title, topic.title);
        assert_eq!(deserialized.summary, None);
        assert_eq!(deserialized.node_ids.len(), 0);
        assert_eq!(deserialized.l3_refs.len(), 0);
        assert_eq!(deserialized.l4_refs.len(), 0);
        assert_eq!(deserialized.parent_id, None);
        assert_eq!(deserialized.is_active, false);
        assert_eq!(deserialized.activation_state, ActivationState::Dormant);
    }

    #[test]
    fn test_topic_slot_with_many_nodes() {
        let node_ids: Vec<u64> = (0..100).collect();
        let topic = TopicSlot {
            id_hash: 111,
            title: "Large Topic".to_string(),
            summary: None,
            node_ids: node_ids.clone(),
            l3_refs: vec![1, 2, 3],
            l4_refs: vec![10, 20, 30, 40],
            parent_id: Some(500),
            created_at: 1000,
            updated_at: 2000,
            version: 1,
            importance: 0.9,
            activation_score: 0.8,
            is_active: true,
            activation_state: ActivationState::Active,
            centroid_vector: None,
            domain_weights: vec![],
            dialogue_range: (1000, 2000),
            reserved: [0; 16],
        };

        let data = topic.serialize().unwrap();
        let deserialized = TopicSlot::deserialize(&data).unwrap();

        assert_eq!(deserialized.node_ids.len(), 100);
        assert_eq!(deserialized.node_ids, node_ids);
        assert_eq!(deserialized.l3_refs, vec![1, 2, 3]);
        assert_eq!(deserialized.l4_refs, vec![10, 20, 30, 40]);
    }

    #[test]
    fn test_topic_slot_unicode_title() {
        let topic = TopicSlot {
            id_hash: 555,
            title: "主题测试 🚀".to_string(),
            summary: Some("摘要内容".to_string()),
            node_ids: vec![1],
            l3_refs: vec![],
            l4_refs: vec![],
            parent_id: None,
            created_at: 1000,
            updated_at: 1000,
            version: 1,
            importance: 0.7,
            activation_score: 0.6,
            is_active: true,
            activation_state: ActivationState::Active,
            centroid_vector: None,
            domain_weights: vec![],
            dialogue_range: (1000, 1000),
            reserved: [0; 16],
        };

        let data = topic.serialize().unwrap();
        let deserialized = TopicSlot::deserialize(&data).unwrap();

        assert_eq!(deserialized.title, topic.title);
        assert_eq!(deserialized.summary, topic.summary);
    }

    #[test]
    fn test_topic_slot_size_calculation() {
        let topic = TopicSlot {
            id_hash: 1,
            title: "test".to_string(),        // 4 bytes
            summary: Some("abc".to_string()), // 3 bytes
            node_ids: vec![1, 2],             // 2 * 8 = 16 bytes
            l3_refs: vec![10],                // 2 + 8 = 10 bytes
            l4_refs: vec![20, 30],            // 2 + 16 = 18 bytes
            parent_id: None,
            created_at: 0,
            updated_at: 0,
            version: 0,
            importance: 0.0,
            activation_score: 0.0,
            is_active: false,
            activation_state: ActivationState::Dormant,
            centroid_vector: None,
            domain_weights: vec![],
            dialogue_range: (0, 0),
            reserved: [0; 16],
        };

        // 70 + 4 + 3 + 16 + 10 + 18 + 8 + 1 + 2 + 16 = 148
        assert_eq!(topic.slot_size(), 148);
    }

    #[test]
    fn test_topic_slot_empty_text() {
        let topic = TopicSlot {
            id_hash: 777,
            title: "".to_string(),
            summary: None,
            node_ids: vec![],
            l3_refs: vec![],
            l4_refs: vec![],
            parent_id: None,
            created_at: 0,
            updated_at: 0,
            version: 0,
            importance: 0.0,
            activation_score: 0.0,
            is_active: false,
            activation_state: ActivationState::Dormant,
            centroid_vector: None,
            domain_weights: vec![],
            dialogue_range: (0, 0),
            reserved: [0; 16],
        };

        let data = topic.serialize().unwrap();
        let deserialized = TopicSlot::deserialize(&data).unwrap();

        assert_eq!(deserialized, topic);
    }
}
