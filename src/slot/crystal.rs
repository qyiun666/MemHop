//! Crystal slot for L5 procedural knowledge (v0.33)
//!
//! Crystal slots store programmatic knowledge in the form of condition-action rules.
//! They represent crystallized patterns that can be triggered during recall operations.

use std::io::{self, Cursor, Read, Write};

/// Crystal status
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum CrystalStatus {
    NotCrystallized = 0,  // Raw operation flow, not yet crystallized
    Crystallized = 1,     // Consolidated into stable skill
}

impl CrystalStatus {
    pub fn from_u8(v: u8) -> Self {
        match v {
            0 => Self::NotCrystallized,
            1 => Self::Crystallized,
            _ => Self::NotCrystallized,
        }
    }
}

/// Crystal slot structure for L5 procedural knowledge
///
/// Stores operation flows for future skill crystallization.
/// - title: crystal title/name
/// - condition: DSL trigger condition
/// - action: operation to execute
/// - raw_steps: original operation steps (for learning)
/// - status: crystallization status
#[derive(Debug, Clone, PartialEq)]
pub struct CrystalSlot {
    /// Hash ID of the crystal
    pub id_hash: u64,
    /// Crystal title/name
    pub title: String,
    /// DSL format condition for triggering
    pub condition: String,
    /// Action to execute when condition is met
    pub action: String,
    /// Raw operation steps (JSON or text format)
    pub raw_steps: String,
    /// Crystallization status
    pub status: CrystalStatus,
    /// Confidence score [0.0, 1.0]
    pub confidence: f32,
    /// Number of times this crystal has been triggered
    pub trigger_count: u32,
    /// Timestamp of last trigger (milliseconds since epoch)
    pub last_triggered: i64,
    /// Creation timestamp (milliseconds since epoch)
    pub created_at: i64,
    /// Version number for schema evolution
    pub version: u32,
}

impl CrystalSlot {
    /// Calculate serialized size in bytes
    ///
    /// Fixed fields: 8 (id_hash) + 2 (cond_len) + 2 (action_len) + 2 (steps_len) +
    ///               4 (confidence) + 4 (trigger_count) + 8 (last_triggered) + 8 (created_at) +
    ///               4 (version) = 42 bytes
    /// Variable fields: condition.len() + action.len() + raw_steps.len()
    pub fn slot_size(&self) -> usize {
        // Fixed: 8(id_hash) + 2(title_len) + 2(cond_len) + 2(action_len) + 2(steps_len) +
        //        1(status) + 4(confidence) + 4(trigger_count) + 8(last_triggered) + 8(created_at) +
        //        4(version) = 45
        let fixed_size = 45;
        fixed_size + self.title.len() + self.condition.len() + self.action.len() + self.raw_steps.len()
    }

    /// Serialize CrystalSlot to bytes
    ///
    /// # Format
    /// - id_hash: u64 (8 bytes, little-endian)
    /// - condition length: u16 (2 bytes, little-endian)
    /// - action length: u16 (2 bytes, little-endian)
    /// - confidence: f32 (4 bytes, little-endian)
    /// - trigger_count: u32 (4 bytes, little-endian)
    /// - last_triggered: i64 (8 bytes, little-endian)
    /// - created_at: i64 (8 bytes, little-endian)
    /// - version: u32 (4 bytes, little-endian)
    /// - condition: UTF-8 bytes
    /// - action: UTF-8 bytes
    ///
    /// # Errors
    /// Returns `io::Error` if write operations fail
    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let mut buffer = Vec::with_capacity(self.slot_size());

        buffer.write_all(&self.id_hash.to_le_bytes())?;

        buffer.write_all(&(self.title.len() as u16).to_le_bytes())?;
        buffer.write_all(&(self.condition.len() as u16).to_le_bytes())?;
        buffer.write_all(&(self.action.len() as u16).to_le_bytes())?;
        buffer.write_all(&(self.raw_steps.len() as u16).to_le_bytes())?;

        buffer.write_all(&[self.status as u8])?;
        buffer.write_all(&self.confidence.to_le_bytes())?;
        buffer.write_all(&self.trigger_count.to_le_bytes())?;
        buffer.write_all(&self.last_triggered.to_le_bytes())?;
        buffer.write_all(&self.created_at.to_le_bytes())?;
        buffer.write_all(&self.version.to_le_bytes())?;

        buffer.write_all(self.title.as_bytes())?;
        buffer.write_all(self.condition.as_bytes())?;
        buffer.write_all(self.action.as_bytes())?;
        buffer.write_all(self.raw_steps.as_bytes())?;

        Ok(buffer)
    }

    /// Deserialize CrystalSlot from bytes
    ///
    /// # Arguments
    /// * `data` - Byte slice containing serialized CrystalSlot data
    ///
    /// # Errors
    /// Returns `io::Error` if:
    /// - Data is too short
    /// - UTF-8 decoding fails for condition or action strings
    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        let mut cursor = Cursor::new(data);

        let read_u64 = |cursor: &mut Cursor<&[u8]>| -> io::Result<u64> {
            let mut buf = [0u8; 8];
            cursor.read_exact(&mut buf)?;
            Ok(u64::from_le_bytes(buf))
        };

        let read_u32 = |cursor: &mut Cursor<&[u8]>| -> io::Result<u32> {
            let mut buf = [0u8; 4];
            cursor.read_exact(&mut buf)?;
            Ok(u32::from_le_bytes(buf))
        };

        let read_i64 = |cursor: &mut Cursor<&[u8]>| -> io::Result<i64> {
            let mut buf = [0u8; 8];
            cursor.read_exact(&mut buf)?;
            Ok(i64::from_le_bytes(buf))
        };

        let read_f32 = |cursor: &mut Cursor<&[u8]>| -> io::Result<f32> {
            let mut buf = [0u8; 4];
            cursor.read_exact(&mut buf)?;
            Ok(f32::from_le_bytes(buf))
        };

        let read_u16 = |cursor: &mut Cursor<&[u8]>| -> io::Result<u16> {
            let mut buf = [0u8; 2];
            cursor.read_exact(&mut buf)?;
            Ok(u16::from_le_bytes(buf))
        };

        let id_hash = read_u64(&mut cursor)?;
        let title_len = read_u16(&mut cursor)?;
        let cond_len = read_u16(&mut cursor)?;
        let action_len = read_u16(&mut cursor)?;
        let steps_len = read_u16(&mut cursor)?;

        let mut status_buf = [0u8; 1];
        cursor.read_exact(&mut status_buf)?;
        let status = CrystalStatus::from_u8(status_buf[0]);

        let confidence = read_f32(&mut cursor)?;
        let trigger_count = read_u32(&mut cursor)?;
        let last_triggered = read_i64(&mut cursor)?;
        let created_at = read_i64(&mut cursor)?;
        let version = read_u32(&mut cursor)?;

        let mut title_buf = vec![0u8; title_len as usize];
        cursor.read_exact(&mut title_buf)?;
        let title = String::from_utf8(title_buf)
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;

        let mut cond_buf = vec![0u8; cond_len as usize];
        cursor.read_exact(&mut cond_buf)?;
        let condition = String::from_utf8(cond_buf)
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;

        let mut action_buf = vec![0u8; action_len as usize];
        cursor.read_exact(&mut action_buf)?;
        let action = String::from_utf8(action_buf)
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;

        let mut steps_buf = vec![0u8; steps_len as usize];
        cursor.read_exact(&mut steps_buf)?;
        let raw_steps = String::from_utf8(steps_buf)
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;

        Ok(CrystalSlot {
            id_hash, title, condition, action, raw_steps, status, confidence,
            trigger_count, last_triggered, created_at, version,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_crystal_roundtrip() {
        let crystal = CrystalSlot {
            id_hash: 123456789,
            title: "Rust Doc Recommendation".to_string(),
            condition: "keyword:Rust AND layer:L1".to_string(),
            action: "recommend_rust_docs".to_string(),
            raw_steps: r#"[{"action":"search","query":"Rust"},{"action":"filter","layer":"L1"}]"#.to_string(),
            status: CrystalStatus::Crystallized,
            confidence: 0.85,
            trigger_count: 5,
            last_triggered: 1000000,
            created_at: 900000,
            version: 1,
        };

        let data = crystal.serialize().unwrap();
        let deserialized = CrystalSlot::deserialize(&data).unwrap();

        assert_eq!(deserialized, crystal);
    }

    #[test]
    fn test_crystal_slot_size() {
        let crystal = CrystalSlot {
            id_hash: 1,
            title: "test".to_string(),
            condition: "test".to_string(),
            action: "act".to_string(),
            raw_steps: "steps".to_string(),
            status: CrystalStatus::NotCrystallized,
            confidence: 0.5,
            trigger_count: 0,
            last_triggered: 0,
            created_at: 0,
            version: 0,
        };

        // 45 + 4 + 4 + 3 + 5 = 61
        assert_eq!(crystal.slot_size(), 61);
    }

    #[test]
    fn test_crystal_empty_strings() {
        let crystal = CrystalSlot {
            id_hash: 999,
            title: "".to_string(),
            condition: "".to_string(),
            action: "".to_string(),
            raw_steps: "".to_string(),
            status: CrystalStatus::NotCrystallized,
            confidence: 1.0,
            trigger_count: 100,
            last_triggered: 0,
            created_at: 0,
            version: 2,
        };

        let data = crystal.serialize().unwrap();
        let deserialized = CrystalSlot::deserialize(&data).unwrap();

        assert_eq!(deserialized, crystal);
        assert_eq!(deserialized.slot_size(), 45);
    }
}
